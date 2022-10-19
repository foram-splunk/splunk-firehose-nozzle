package splunknozzle

import (
	"os"
	"strings"
	"time"

	"code.cloudfoundry.org/lager"
	cfclient "github.com/cloudfoundry-community/go-cfclient"
	"github.com/cloudfoundry-community/splunk-firehose-nozzle/cache"
	"github.com/cloudfoundry-community/splunk-firehose-nozzle/eventrouter"
	"github.com/cloudfoundry-community/splunk-firehose-nozzle/events"
	"github.com/cloudfoundry-community/splunk-firehose-nozzle/eventsink"
	"github.com/cloudfoundry-community/splunk-firehose-nozzle/eventsource"
	"github.com/cloudfoundry-community/splunk-firehose-nozzle/eventwriter"
	"github.com/cloudfoundry-community/splunk-firehose-nozzle/monitoring"
	"github.com/cloudfoundry-community/splunk-firehose-nozzle/utils"

	"github.com/cloudfoundry-community/splunk-firehose-nozzle/nozzle"
	"github.com/google/uuid"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
)

type SplunkFirehoseNozzle struct {
	config *Config
	logger lager.Logger
}

//create new function of type *SplunkFirehoseNozzle
func NewSplunkFirehoseNozzle(config *Config, logger lager.Logger) *SplunkFirehoseNozzle {
	return &SplunkFirehoseNozzle{
		config: config,
		logger: logger,
	}
}

// EventRouter creates EventRouter object and setup routes for interested events
func (s *SplunkFirehoseNozzle) EventRouter(cache cache.Cache, eventSink eventsink.Sink) (eventrouter.Router, error) {
	LowerAddAppInfo := strings.ToLower(s.config.AddAppInfo)
	config := &eventrouter.Config{
		SelectedEvents: s.config.WantedEvents,
		AddAppName:     strings.Contains(LowerAddAppInfo, "appname"),
		AddOrgName:     strings.Contains(LowerAddAppInfo, "orgname"),
		AddOrgGuid:     strings.Contains(LowerAddAppInfo, "orgguid"),
		AddSpaceName:   strings.Contains(LowerAddAppInfo, "spacename"),
		AddSpaceGuid:   strings.Contains(LowerAddAppInfo, "spaceguid"),
		AddTags:        s.config.AddTags,
	}
	return eventrouter.New(cache, eventSink, config)
}

// CFClient creates a client object which can talk to Cloud Foundry
func (s *SplunkFirehoseNozzle) PCFClient() (*cfclient.Client, error) {
	cfConfig := &cfclient.Config{
		ApiAddress:        s.config.ApiEndpoint,
		Username:          s.config.User,
		Password:          s.config.Password,
		SkipSslValidation: s.config.SkipSSLCF,
		ClientID:          s.config.ClientID,
		ClientSecret:      s.config.ClientSecret,
	}

	return cfclient.NewClient(cfConfig)
}

// AppCache creates in-memory cache or boltDB cache
func (s *SplunkFirehoseNozzle) AppCache(client cache.AppClient) (cache.Cache, error) {
	if s.config.AddAppInfo != "" {
		c := cache.BoltdbConfig{
			Path:               s.config.BoltDBPath,
			IgnoreMissingApps:  s.config.IgnoreMissingApps,
			MissingAppCacheTTL: s.config.MissingAppCacheTTL,
			AppCacheTTL:        s.config.AppCacheTTL,
			OrgSpaceCacheTTL:   s.config.OrgSpaceCacheTTL,
			Logger:             s.logger,
		}
		return cache.NewBoltdb(client, &c)
	}

	return cache.NewNoCache(), nil
}

// EventSink creates std sink or Splunk sink
func (s *SplunkFirehoseNozzle) EventSink(cache cache.Cache) (eventsink.Sink, error) {

	// EventWriter for writing events
	writerConfig := &eventwriter.SplunkConfig{
		Host:        s.config.SplunkHost,
		Token:       s.config.SplunkToken,
		Index:       s.config.SplunkIndex,
		SkipSSL:     s.config.SkipSSLSplunk,
		Debug:       s.config.Debug,
		Logger:      s.logger,
		Version:     s.config.Version,
		MetricIndex: s.config.SplunkMetricIndex,
	}

	var writers []eventwriter.Writer
	for i := 0; i < s.config.HecWorkers+1; i++ {
		splunkWriter := eventwriter.NewSplunkEvent(writerConfig).(*eventwriter.SplunkEvent)
		splunkWriter.SentEventCount = monitoring.RegisterCounter("splunk.events.sent.count", utils.UintType)
		splunkWriter.BodyBufferSize = monitoring.RegisterCounter("splunk.events.throughput", utils.UintType)
		writers = append(writers, splunkWriter)
	}

	parsedExtraFields, err := events.ParseExtraFields(s.config.ExtraFields)
	if err != nil {
		s.logger.Error("Error at parsing extra fields", nil)
		return nil, err
	}

	nozzleUUID := uuid.New().String()

	sinkConfig := &eventsink.SplunkConfig{
		FlushInterval:         s.config.FlushInterval,
		QueueSize:             s.config.QueueSize,
		BatchSize:             s.config.BatchSize,
		Retries:               s.config.Retries,
		Hostname:              s.config.JobHost,
		SubscriptionID:        s.config.SubscriptionID,
		TraceLogging:          s.config.TraceLogging,
		ExtraFields:           parsedExtraFields,
		UUID:                  nozzleUUID,
		Logger:                s.logger,
		StatusMonitorInterval: s.config.StatusMonitorInterval,
	}

	LowerAddAppInfo := strings.ToLower(s.config.AddAppInfo)
	parseConfig := &eventsink.ParseConfig{
		SelectedEvents: s.config.WantedEvents,
		AddAppName:     strings.Contains(LowerAddAppInfo, "appname"),
		AddOrgName:     strings.Contains(LowerAddAppInfo, "orgname"),
		AddOrgGuid:     strings.Contains(LowerAddAppInfo, "orgguid"),
		AddSpaceName:   strings.Contains(LowerAddAppInfo, "spacename"),
		AddSpaceGuid:   strings.Contains(LowerAddAppInfo, "spaceguid"),
		AddTags:        s.config.AddTags,
	}

	splunkSink := eventsink.NewSplunk(writers, sinkConfig, parseConfig, cache)
	splunkSink.Open()

	s.logger.RegisterSink(splunkSink)
	if s.config.StatusMonitorInterval > time.Second*0 {
		go splunkSink.LogStatus()
	}
	return splunkSink, nil
}

func (s *SplunkFirehoseNozzle) Metric() monitoring.Monitor {

	writerConfig := &eventwriter.SplunkConfig{
		Host:        s.config.SplunkHost,
		Token:       s.config.SplunkToken,
		Index:       s.config.SplunkMetricIndex,
		SkipSSL:     s.config.SkipSSLSplunk,
		Debug:       s.config.Debug,
		Logger:      s.logger,
		Version:     s.config.Version,
		MetricIndex: s.config.SplunkMetricIndex,
	}
	if s.config.StatusMonitorInterval > 0*time.Second && s.config.SelectedMonitoringMetrics != "" {

		monitoring.RegisterFunc("nozzle.usage.ram", func() interface{} {
			v, _ := mem.VirtualMemory()
			return (v.UsedPercent)
		})

		monitoring.RegisterFunc("nozzle.usage.cpu", func() interface{} {
			CPU, _ := cpu.Percent(0, false)
			return (CPU[0])
		})

		splunkWriter := eventwriter.NewSplunkMetric(writerConfig)
		return monitoring.NewMetricsMonitor(s.logger, s.config.StatusMonitorInterval, splunkWriter, s.config.SelectedMonitoringMetrics)
	} else {
		return monitoring.NewNoMonitor()
	}

}

// EventSource creates eventsource.Source object which can read events from
func (s *SplunkFirehoseNozzle) EventSource(pcfClient *cfclient.Client) *eventsource.Firehose {
	config := &eventsource.FirehoseConfig{
		KeepAlive:      s.config.KeepAlive,
		SkipSSL:        s.config.SkipSSLCF,
		Endpoint:       pcfClient.Endpoint.DopplerEndpoint,
		SubscriptionID: s.config.SubscriptionID,
	}

	return eventsource.NewFirehose(pcfClient, config)
}

// Nozzle creates a Nozzle object which glues the event source and event router
func (s *SplunkFirehoseNozzle) Nozzle(eventSource eventsource.Source, eventRouter eventrouter.Router) *nozzle.Nozzle {
	firehoseConfig := &nozzle.Config{
		Logger:                s.logger,
		StatusMonitorInterval: s.config.StatusMonitorInterval,
	}

	return nozzle.New(eventSource, eventRouter, firehoseConfig)
}

// Run creates all necessary objects, reading events from CF firehose and sending to target Splunk index
// It runs forever until something goes wrong
func (s *SplunkFirehoseNozzle) Run(shutdownChan chan os.Signal) error {

	metric := s.Metric()

	pcfClient, err := s.PCFClient()
	if err != nil {
		s.logger.Error("Failed to get info from CF Server", nil)
		return err
	}

	appCache, err := s.AppCache(pcfClient)
	if err != nil {
		s.logger.Error("Failed to start App Cache", nil)
		return err
	}

	err = appCache.Open()
	if err != nil {
		s.logger.Error("Failed to open App Cache", nil)
		return err
	}
	defer appCache.Close()

	eventSink, err := s.EventSink(appCache)
	if err != nil {
		s.logger.Error("Failed to create event sink", nil)
		return err
	}

	s.logger.Info("Running splunk-firehose-nozzle with following configuration variables ", s.config.ToMap())

	eventRouter, err := s.EventRouter(appCache, eventSink)
	if err != nil {
		s.logger.Error("Failed to create event router", nil)
		return err
	}

	eventSource := s.EventSource(pcfClient)
	noz := s.Nozzle(eventSource, eventRouter)

	// Continuous Loop will run forever
	go func() {
		err := noz.Start()
		if err != nil {
			s.logger.Error("Firehose consumer exits with error", err)
		}
		shutdownChan <- os.Interrupt
	}()

	go metric.Start()

	<-shutdownChan

	s.logger.Info("Splunk Nozzle is going to exit gracefully")
	metric.Stop()
	noz.Close()
	return eventSink.Close()
}
