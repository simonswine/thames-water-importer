package app

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	retry "github.com/avast/retry-go/v4"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/grafana/dskit/runutil"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/timestamp"
	"github.com/prometheus/prometheus/tsdb"
	"github.com/thanos-io/thanos/pkg/block/metadata"
	"github.com/thanos-io/thanos/pkg/objstore/client"
	"github.com/thanos-io/thanos/pkg/shipper"

	"github.com/simonswine/thames-water-importer/api"
)

const (
	loginURL = "https://myaccount.thameswater.co.uk/login"
)

type logLevelOverride struct {
	next  log.Logger
	level interface{}
}

func (l *logLevelOverride) Log(keyvals ...interface{}) error {
	for i := 0; i < len(keyvals); i += 2 {
		if n, ok := keyvals[0].(string); ok && n == "level" {
			keyvals[i+1] = l.level
			return l.next.Log(keyvals...)
		}
	}
	kvs := make([]interface{}, len(keyvals)+2)
	kvs[0], kvs[1] = level.Key(), l.level
	copy(kvs[2:], keyvals)
	return l.next.Log(kvs...)
}

type config struct {
	thamesWaterEmail        string
	thamesWaterPassword     string
	thamesWaterLoginTimeout time.Duration

	chromeHeadless bool
	chromeSandbox  bool

	tsdbPath          string
	tsdbBlockDuration time.Duration

	externalLabels func() labels.Labels

	thanosBucketObj []byte
}

func defaultConfig() *config {
	return &config{
		externalLabels: func() labels.Labels {
			return labels.FromStrings("cluster", "thames-water-importer")
		},

		chromeSandbox:  true,
		chromeHeadless: true,

		tsdbPath:          "./tsdb",
		tsdbBlockDuration: 2 * time.Hour,
	}
}

type App struct {
	logger log.Logger
	reg    *prometheus.Registry
	cfg    *config
}

type NewOption func(*App)

func WithLogger(l log.Logger) NewOption {
	return func(a *App) {
		a.logger = l
	}
}

func WithThamesWaterLogin(email, password string) NewOption {
	return func(a *App) {
		a.cfg.thamesWaterEmail = email
		a.cfg.thamesWaterPassword = password
	}
}

func WithThamesWaterLoginTimeout(d time.Duration) NewOption {
	return func(a *App) {
		a.cfg.thamesWaterLoginTimeout = d
	}
}

func WithChromeHeadless(b bool) NewOption {
	return func(a *App) {
		a.cfg.chromeHeadless = b
	}
}
func WithChromeSandbox(b bool) NewOption {
	return func(a *App) {
		a.cfg.chromeSandbox = b
	}
}

func WithTSDBPath(s string) NewOption {
	return func(a *App) {
		a.cfg.tsdbPath = s
	}
}

func WithTSDBBlockDuration(d time.Duration) NewOption {
	return func(a *App) {
		a.cfg.tsdbBlockDuration = d
	}
}

func WithExternalLabels(strs ...string) NewOption {
	return func(a *App) {
		a.cfg.externalLabels = func() labels.Labels {
			return labels.FromStrings(strs...)
		}
	}
}

func WithThanosBucketObj(str string) NewOption {
	return func(a *App) {
		a.cfg.thanosBucketObj = []byte(str)
	}
}

func New(opts ...NewOption) *App {
	a := &App{
		reg:    prometheus.NewRegistry(),
		logger: log.NewNopLogger(),
		cfg:    defaultConfig(),
	}

	for _, o := range opts {
		o(a)
	}

	return a
}

// uploadLocalTSDB uploads the local TSDB blocks generated using a thanos shipper component
func (a *App) uploadLocalTSDB(ctx context.Context) error {
	source := metadata.SourceType("importer")

	bkt, err := client.NewBucket(a.logger, a.cfg.thanosBucketObj, a.reg, string(source))
	if err != nil {
		return err
	}
	// Ensure we close up everything properly.
	defer func() {
		if err != nil {
			runutil.CloseWithLogOnErr(a.logger, bkt, "bucket client")
		}
	}()

	// upload new blocks
	s := shipper.New(
		a.logger,
		a.reg,
		a.cfg.tsdbPath,
		bkt,
		a.cfg.externalLabels,
		source,
		true,
		true,
		metadata.SHA256Func,
	)

	n, err := s.Sync(ctx)
	if err != nil {
		return err
	}

	_ = level.Info(a.logger).Log("msg", fmt.Sprintf("successfully uploaded %d blocks", n))
	return nil
}

func (a *App) getLoginCookies(ctx context.Context) ([]*http.Cookie, error) {
	opts := chromedp.DefaultExecAllocatorOptions[:]

	if !a.cfg.chromeSandbox {
		opts = append(opts, chromedp.NoSandbox)
	}

	if !a.cfg.chromeHeadless {
		opts = append(opts, chromedp.Flag("headless", false))
	}

	allocCtx, _ := chromedp.NewExecAllocator(ctx, opts...)

	// create context
	chromeCtx, cancel := chromedp.NewContext(
		allocCtx,
	)
	defer cancel()

	var accountNumber, accountAddress string
	var twCookies []*http.Cookie

	// login to thames water
	_ = level.Info(a.logger).Log("msg", "attempting login to thames water account", "email", a.cfg.thamesWaterEmail)
	if err := chromedp.Run(chromeCtx,
		loginThamesWater(a.logger, a.cfg.thamesWaterEmail, a.cfg.thamesWaterPassword, &accountNumber, &accountAddress),
		chromedp.ActionFunc(func(ctx context.Context) error {
			cookies, err := network.GetAllCookies().Do(ctx)
			if err != nil {
				return err
			}

			for _, cookie := range cookies {
				if strings.HasSuffix(cookie.Domain, ".thameswater.co.uk") && (cookie.Name == "JSESSIONID" || cookie.Name == "da_sid" || cookie.Name == "da_lid" || cookie.Name == "ARRAffinity" || cookie.Name == "ARRAffinitySameSite") {
					twCookies = append(twCookies, &http.Cookie{
						Name:  cookie.Name,
						Value: cookie.Value,

						Path:   cookie.Path,
						Domain: cookie.Domain,
						Expires: func() time.Time {
							if cookie.Expires < 0 {
								return time.Time{}
							}
							return time.Unix(int64(cookie.Expires), 0)
						}(),
						Secure: cookie.Secure,
						SameSite: func() http.SameSite {
							switch cookie.SameSite {
							case network.CookieSameSiteLax:
								return http.SameSiteLaxMode
							case network.CookieSameSiteStrict:
								return http.SameSiteStrictMode
							case network.CookieSameSiteNone:
								return http.SameSiteNoneMode
							}
							return http.SameSiteDefaultMode
						}(),
					})
				}
			}

			return nil
		}),
	); err != nil {
		return nil, err
	}
	_ = level.Info(a.logger).Log("msg", "successfully logged in", "accountNumber", accountNumber, "accountAddress", accountAddress)

	return twCookies, nil
}

func (a *App) importConsumptionIntoLocalTSDB(ctx context.Context) error {
	// open tsdb
	options := tsdb.DefaultOptions()
	options.RetentionDuration = 90 * 24 * time.Hour.Milliseconds()

	// set retention
	options.MinBlockDuration = a.cfg.tsdbBlockDuration.Milliseconds()
	options.MaxBlockDuration = a.cfg.tsdbBlockDuration.Milliseconds()

	db, err := tsdb.Open(a.cfg.tsdbPath, &logLevelOverride{next: a.logger, level: level.DebugValue()}, a.reg, options, nil)
	if err != nil {
		return err
	}
	defer db.Close()

	var (
		minTime, maxTime time.Time
	)
	if mT, init := db.Head().AppendableMinValidTime(); init {
		minTime = timestamp.Time(mT)
		maxTime = timestamp.Time(db.Head().MaxTime())
		_ = level.Debug(a.logger).Log("msg", "opened TSDB",
			"min_time", minTime,
			"max_time", maxTime,
		)
	}

	var twCookies []*http.Cookie

	if err := retry.Do(
		func() error {
			ctx, cancel := context.WithTimeout(ctx, a.cfg.thamesWaterLoginTimeout)
			defer cancel()

			var err error
			twCookies, err = a.getLoginCookies(ctx)

			return err
		},
		retry.Context(ctx),
		retry.OnRetry(func(n uint, err error) {
			_ = a.logger.Log("msg", "login failed", "err", err, "try", n+1)
		}),
	); err != nil {
		return err
	}

	twClient, err := api.New(twCookies)
	if err != nil {
		return err
	}

	resp, err := twClient.GetMeters(ctx)
	if err != nil {
		return err
	}

	if len(resp.Meters) == 0 {
		return fmt.Errorf("no meters found")
	}

	_ = level.Info(a.logger).Log("msg", "found meters", "meters", strings.Join(resp.Meters, ", "))

	meter := resp.Meters[0]

	var readingRequests = make([]api.GetSmartWaterMeterConsumptionsRequest, len(resp.Daily))
	for pos := range resp.Daily {
		readingRequests[pos].Meter = meter

		ts, err := time.Parse("02-01-2006", resp.Daily[pos].Value)
		if err != nil {
			return err
		}
		readingRequests[pos].StartDate = ts
		readingRequests[pos].EndDate = ts
	}
	sort.Slice(readingRequests, func(i, j int) bool {
		return readingRequests[i].StartDate.Before(readingRequests[j].StartDate)
	})

	// prepare labels
	lbls := labels.NewBuilder(a.cfg.externalLabels())
	lbls.Set("job", "thames-water-importer")
	lbls.Set(labels.MetricName, "water_consumption_liters")

	for _, reqData := range readingRequests {
		if !minTime.Before(reqData.StartDate) {
			_ = level.Debug(a.logger).Log("msg", "skipped daily reading, as TSDB already contains data", "meter", reqData.Meter, "date", reqData.StartDate.Format("2006-01-02"))
			continue
		}
		_ = level.Debug(a.logger).Log("msg", "daily reading", "meter", reqData.Meter, "date", reqData.StartDate.Format("2006-01-02"))

		resp, err := twClient.GetSmartWaterMeterConsumptions(ctx, reqData)
		if err != nil {
			return err
		}

		// get new appender to TSDB
		a := db.Appender(ctx)

		for pos := range resp.Lines {
			timeParts := strings.Split(resp.Lines[pos].Label, ":")
			if len(timeParts) != 2 {
				return fmt.Errorf("unexpected label split count: %d", len(timeParts))
			}
			hours, err := strconv.ParseInt(timeParts[0], 10, 32)
			if err != nil {
				return err
			}
			minutes, err := strconv.ParseInt(timeParts[1], 10, 32)
			if err != nil {
				return err
			}

			ts := time.Date(
				reqData.StartDate.Year(),
				reqData.StartDate.Month(),
				reqData.StartDate.Day(),
				int(hours),
				int(minutes),
				0,
				0,
				time.UTC,
			)
			lbls.Set("meter", resp.Lines[pos].MeterSerialNumberHis)
			if _, err := a.Append(
				0,
				lbls.Labels(),
				timestamp.FromTime(ts),
				resp.Lines[pos].Read,
			); err != nil {
				return err
			}
		}

		if err := a.Commit(); err != nil {
			return err
		}
	}

	if err := db.Compact(); err != nil {
		return fmt.Errorf("error during compaction: %w", err)
	}
	_ = level.Debug(a.logger).Log("msg", "ran TSDB compaction")

	return nil
}

func loginThamesWater(logger log.Logger, email, password string, accountNumber, accountAddress *string) chromedp.Tasks {
	return chromedp.Tasks{
		// open url
		chromedp.Navigate(loginURL),

		chromedp.ActionFunc(func(ctx context.Context) error {
			// force viewport emulation
			return emulation.SetDeviceMetricsOverride(1280, 1024, 1, false).
				WithScreenOrientation(&emulation.ScreenOrientation{
					Type:  emulation.OrientationTypePortraitPrimary,
					Angle: 0,
				}).
				Do(ctx)
		}),

		// accept cookie
		chromedp.ActionFunc(func(context.Context) error {
			return level.Debug(logger).Log("msg", "waiting for cookie consent", "url", loginURL)
		}),
		chromedp.WaitVisible(`button#onetrust-accept-btn-handler`),
		chromedp.Sleep(2 * time.Second), // wait for animation to finish

		chromedp.Click(`button#onetrust-accept-btn-handler`),
		chromedp.WaitNotVisible(`button#onetrust-accept-btn-handler`),

		// enter email
		chromedp.ActionFunc(func(context.Context) error {
			return level.Debug(logger).Log("msg", "enter email", "email", email)
		}),
		chromedp.SendKeys(`//input[@type="email" and @id="email"]`, email),

		// enter password
		chromedp.ActionFunc(func(context.Context) error {
			return level.Debug(logger).Log("msg", "enter password", "password", strings.Repeat("*", len(password)))
		}),
		chromedp.SendKeys(`//input[@type="password" and @id="password"]`, password),
		chromedp.Click(`button#next`, chromedp.NodeVisible),

		// wait for account details to be shown (otherwise cookie is not authorized)
		chromedp.ActionFunc(func(context.Context) error {
			return level.Debug(logger).Log("msg", "wait for account details to be shown")
		}),
		chromedp.WaitReady(`div.details-panel`),

		// extract account number / address
		chromedp.Text(`div.details-panel span.detail-value.txt-actnumber`, accountNumber),
		chromedp.Text(`div.details-panel span.detail-value.txt-adr`, accountAddress),
	}
}

func (a *App) Run(ctx context.Context) error {
	if err := a.importConsumptionIntoLocalTSDB(ctx); err != nil {
		return err
	}

	if err := a.uploadLocalTSDB(ctx); err != nil {
		return err
	}

	return nil
}
