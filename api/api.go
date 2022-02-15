package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"time"

	"golang.org/x/net/publicsuffix"
)

const (
	dashboardURL                      = "https://myaccount.thameswater.co.uk/mydashboard/my-meters-usage"
	getMetersURL                      = "https://myaccount.thameswater.co.uk/ajax/waterMeter/getMeters"
	getSmartWaterMeterConsumptionsURL = "https://myaccount.thameswater.co.uk/ajax/waterMeter/getSmartWaterMeterConsumptions"
)

type additionalHeaders struct {
	http.Header
}

func (a *additionalHeaders) RoundTrip(req *http.Request) (*http.Response, error) {
	for key, val := range a.Header {
		req.Header[key] = val
	}
	return http.DefaultTransport.RoundTrip(req)
}

func New(cookies []*http.Cookie) (*Client, error) {
	var h = additionalHeaders{make(http.Header)}
	h.Set("x-requested-with", "XMLHttpRequest")
	h.Set("referer", dashboardURL)

	u, err := url.Parse(dashboardURL)
	if err != nil {
		return nil, err
	}

	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return nil, err
	}

	jar.SetCookies(u, append(cookies, []*http.Cookie{
		{
			Name:   "LoggedIntoMyAccount",
			Value:  "1",
			Domain: u.Hostname(),
			Path:   "/",
			Secure: true,
		},
		{
			Name:   "loginCount",
			Value:  "1",
			Domain: u.Hostname(),
			Path:   "/",
			Secure: true,
		},
	}...))

	return &Client{
		httpClient: &http.Client{
			Jar:       jar,
			Transport: &h,
		},
	}, nil
}

type Client struct {
	httpClient *http.Client
}

type Reading struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type GetMetersResponse struct {
	Yearly                               []Reading   `json:"Yearly"`
	HalfYearly                           []Reading   `json:"HalfYearly"`
	Monthly                              []Reading   `json:"Monthly"`
	Daily                                []Reading   `json:"Daily"`
	Meters                               []string    `json:"Meters"`
	IsRecentCustomer                     bool        `json:"IsRecentCustomer"`
	IsPremiseAddressSameAsMailingAddress bool        `json:"IsPremiseAddressSameAsMailingAddress"`
	IsError                              bool        `json:"IsError"`
	IsDataAvailable                      bool        `json:"IsDataAvailable"`
	Lines                                interface{} `json:"Lines"`
	IsConsumptionAvailable               bool        `json:"IsConsumptionAvailable"`
	AlertsValues                         interface{} `json:"AlertsValues"`
	TargetUsage                          float64     `json:"TargetUsage"`
	AverageUsage                         float64     `json:"AverageUsage"`
	ActualUsage                          float64     `json:"ActualUsage"`
	MyUsage                              interface{} `json:"MyUsage"`
	AverageUsagePerPerson                float64     `json:"AverageUsagePerPerson"`
}

func (c *Client) GetMeters(ctx context.Context) (*GetMetersResponse, error) {
	resp, err := c.httpClient.Get(getMetersURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	var meters GetMetersResponse

	if err := json.NewDecoder(resp.Body).Decode(&meters); err != nil {
		return nil, err
	}

	return &meters, nil
}

type GetSmartWaterMeterConsumptionsRequest struct {
	Meter     string
	StartDate time.Time
	EndDate   time.Time
}

type SmartWaterMeterReading struct {
	Label                string  `json:"Label"`
	Usage                float64 `json:"Usage"`
	Read                 float64 `json:"Read"`
	IsEstimated          bool    `json:"IsEstimated"`
	MeterSerialNumberHis string  `json:"MeterSerialNumberHis"`
}

type GetSmartWaterMeterConsumptionsResponse struct {
	IsError                bool                     `json:"IsError"`
	IsDataAvailable        bool                     `json:"IsDataAvailable"`
	Lines                  []SmartWaterMeterReading `json:"Lines"`
	IsConsumptionAvailable bool                     `json:"IsConsumptionAvailable"`
	AlertsValues           interface{}              `json:"AlertsValues"`
	TargetUsage            float64                  `json:"TargetUsage"`
	AverageUsage           float64                  `json:"AverageUsage"`
	ActualUsage            float64                  `json:"ActualUsage"`
	MyUsage                string                   `json:"MyUsage"`
	AverageUsagePerPerson  float64                  `json:"AverageUsagePerPerson"`
}

func (c *Client) GetSmartWaterMeterConsumptions(ctx context.Context, req GetSmartWaterMeterConsumptionsRequest) (*GetSmartWaterMeterConsumptionsResponse, error) {
	u, err := url.Parse(getSmartWaterMeterConsumptionsURL)
	if err != nil {
		return nil, err
	}

	values := u.Query()
	values.Set("meter", req.Meter)
	values.Set("startDate", fmt.Sprintf("%d", req.StartDate.Day()))
	values.Set("startMonth", fmt.Sprintf("%d", req.StartDate.Month()))
	values.Set("startYear", fmt.Sprintf("%d", req.StartDate.Year()))
	values.Set("endDate", fmt.Sprintf("%d", req.EndDate.Day()))
	values.Set("endMonth", fmt.Sprintf("%d", req.EndDate.Month()))
	values.Set("endYear", fmt.Sprintf("%d", req.EndDate.Year()))
	values.Set("granularity", "H")
	values.Set("premiseId", "")
	u.RawQuery = values.Encode()

	resp, err := c.httpClient.Get(u.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	var readings GetSmartWaterMeterConsumptionsResponse

	if err := json.NewDecoder(resp.Body).Decode(&readings); err != nil {
		return nil, err
	}

	return &readings, nil
}
