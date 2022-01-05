// Command click is a chromedp example demonstrating how to use a selector to
// click on an element.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/simonswine/thames-water-importer/app"
	"github.com/urfave/cli/v2"
)

func main() {
	var (
		logger = log.With(
			log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr)),
			"ts", log.DefaultTimestampUTC,
			"caller", log.DefaultCaller,
		)
	)

	cliApp := &cli.App{
		Name:  "thames-water-importer",
		Usage: "Export Thames Water Smartmeter consumption data and ingest into Thanos",
		Action: func(c *cli.Context) error {

			var externalLabels []string
			for _, lbl := range c.StringSlice("external-labels") {
				parts := strings.Split(lbl, "=")
				if len(parts) != 2 {
					return fmt.Errorf("invalid label '%s'", lbl)
				}
				externalLabels = append(externalLabels, parts[0], parts[1])
			}

			a := app.New(
				app.WithLogger(logger),
				app.WithThamesWaterLogin(c.String("thames-water-email"), c.String("thames-water-password")),
				app.WithThamesWaterLoginTimeout(c.Duration("thames-water-login-timeout")),
				app.WithChromeHeadless(c.Bool("chrome-headless")),
				app.WithChromeSandbox(c.Bool("chrome-sandbox")),
				app.WithTSDBPath(c.String("tsdb-path")),
				app.WithTSDBBlockDuration(c.Duration("tsdb-block-duration")),
				app.WithExternalLabels(externalLabels...),
				app.WithThanosBucketObj(c.String("thanos-bucket-obj")),
			)

			ctx := context.Background()
			return a.Run(ctx)
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "thames-water-email",
				Usage:    "Thames Water online account email address.",
				EnvVars:  []string{"THAMES_WATER_EMAIL"},
				Required: true,
			},
			&cli.DurationFlag{
				Name:  "thames-water-login-timeout",
				Usage: "Configure the TSDB block length. Only change if you know what you are doing.",
				Value: 10 * time.Second,
			},
			&cli.StringFlag{
				Name:        "thames-water-password",
				Usage:       "Thames Water online account password.",
				EnvVars:     []string{"THAMES_WATER_PASSWORD"},
				Required:    true,
				DefaultText: "none",
			},
			&cli.PathFlag{
				Name:  "tsdb-path",
				Usage: "Configure the path to the TSDB stoarge.",
				Value: "./tsdb",
			},
			&cli.DurationFlag{
				Name:  "tsdb-block-length",
				Usage: "Configure the TSDB block length. Only change if you know what you are doing.",
				Value: 2 * time.Hour,
			},
			&cli.BoolFlag{
				Name:  "chrome-sandbox",
				Usage: "This allows to disable the Chrome sandbox. This makes it easier to run in a container.",
				Value: true,
			},
			&cli.BoolFlag{
				Name:  "chrome-headless",
				Usage: "This allows to enable the Chrome UI for debugging.",
				Value: true,
			},
			&cli.StringSliceFlag{
				Name:  "external-labels",
				Usage: "External labels are added to the metrics in each block to identify them",
				Value: cli.NewStringSlice("cluster=thames-water-importer"),
			},
			&cli.StringFlag{
				Name:        "thanos-bucket-obj",
				Usage:       "Thanos object store bucket object.",
				EnvVars:     []string{"THANOS_BUCKET_OBJ"},
				Required:    true,
				DefaultText: "none",
			},
		},
	}

	err := cliApp.Run(os.Args)
	if err != nil {
		_ = level.Error(logger).Log("msg", err)
		os.Exit(1)
	}
}
