package nozzle

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"time"

	"github.com/cloudfoundry/sonde-go/events"
	"github.com/pivotal-golang/lager"
)

type Nozzle interface {
	Run(flushWindow time.Duration) error
}

type SplunkNozzle struct {
	splunkClient       SplunkClient
	includedEventTypes map[events.Envelope_EventType]bool
	eventsChannel      <-chan *events.Envelope
	errorsChannel      <-chan error
	batch              []*SplunkEvent
	logger             lager.Logger
}

func NewSplunkForwarder(splunkClient SplunkClient, selectedEventTypes []events.Envelope_EventType, eventsChannel <-chan *events.Envelope, errors <-chan error, logger lager.Logger) Nozzle {
	splunkNozzle := &SplunkNozzle{
		splunkClient:  splunkClient,
		eventsChannel: eventsChannel,
		errorsChannel: errors,
		batch:         []*SplunkEvent{},
		logger:        logger,
	}

	splunkNozzle.includedEventTypes = map[events.Envelope_EventType]bool{
		events.Envelope_HttpStart:       false,
		events.Envelope_HttpStop:        false,
		events.Envelope_HttpStartStop:   false,
		events.Envelope_LogMessage:      false,
		events.Envelope_ValueMetric:     false,
		events.Envelope_CounterEvent:    false,
		events.Envelope_Error:           false,
		events.Envelope_ContainerMetric: false,
	}
	for _, selectedEventType := range selectedEventTypes {
		splunkNozzle.includedEventTypes[selectedEventType] = true
	}

	return splunkNozzle
}

func (s *SplunkNozzle) Run(flushWindow time.Duration) error {
	ticker := time.Tick(flushWindow)
	for {
		select {
		case err := <-s.errorsChannel:
			return err
		case event := <-s.eventsChannel:
			s.handleEvent(event)
		case <-ticker:
			if len(s.batch) > 0 {
				s.logger.Info(fmt.Sprintf("Posting %d events", len(s.batch)))
				s.splunkClient.PostBatch(s.batch)
				s.batch = []*SplunkEvent{}
			}
		}
	}
}

func (s *SplunkNozzle) handleEvent(event *events.Envelope) {
	var splunkEvent *SplunkEvent = nil

	eventType := event.GetEventType()
	if !s.includedEventTypes[eventType] {
		return
	}

	switch eventType {
	case events.Envelope_HttpStart:
	case events.Envelope_HttpStop:
	case events.Envelope_HttpStartStop:
		splunkEvent = BuildHttpStartStopMetric(event)
	case events.Envelope_LogMessage:
		splunkEvent = BuildLogMessageMetric(event)
	case events.Envelope_ValueMetric:
		splunkEvent = BuildValueMetric(event)
	case events.Envelope_CounterEvent:
		splunkEvent = BuildCounterEventMetric(event)
	case events.Envelope_Error:
		splunkEvent = BuildErrorMetric(event)
	case events.Envelope_ContainerMetric:
		splunkEvent = BuildContainerMetric(event)
	}

	if splunkEvent != nil {
		s.batch = append(s.batch, splunkEvent)
	}
}

type CommonMetricFields struct {
	Deployment string `json:"deployment"`
	Index      string `json:"index"`
	EventType  string `json:"eventType"`
}

func buildSplunkMetric(nozzleEvent *events.Envelope, shared *CommonMetricFields) *SplunkEvent {
	shared.Deployment = nozzleEvent.GetDeployment()
	shared.Index = nozzleEvent.GetIndex()
	shared.EventType = nozzleEvent.GetEventType().String()

	splunkEvent := &SplunkEvent{
		Time:   nanoSecondsToSeconds(nozzleEvent.GetTimestamp()),
		Host:   nozzleEvent.GetIp(),
		Source: nozzleEvent.GetJob(),
	}
	return splunkEvent
}

type SplunkHttpStartStopMetric struct {
	CommonMetricFields
	StartTimestamp int64    `json:"startTimestamp"`
	StopTimestamp  int64    `json:"stopTimestamp"`
	RequestId      string   `json:"requestId"`
	PeerType       string   `json:"peerType"`
	Method         string   `json:"method"`
	Uri            string   `json:"uri"`
	RemoteAddress  string   `json:"remoteAddress"`
	UserAgent      string   `json:"userAgent"`
	StatusCode     int32    `json:"statusCode"`
	ContentLength  int64    `json:"contentLength"`
	ApplicationId  string   `json:"applicationId"`
	InstanceIndex  int32    `json:"instanceIndex"`
	Forwarded      []string `json:"forwarded"`
}

func BuildHttpStartStopMetric(nozzleEvent *events.Envelope) *SplunkEvent {
	startStop := nozzleEvent.HttpStartStop

	splunkHttpStartStopMetric := SplunkHttpStartStopMetric{
		StartTimestamp: startStop.GetStartTimestamp(),
		StopTimestamp:  startStop.GetStopTimestamp(),
		RequestId:      uuidToHex(startStop.GetRequestId()),
		PeerType:       startStop.GetPeerType().String(),
		Method:         startStop.GetMethod().String(),
		Uri:            startStop.GetUri(),
		RemoteAddress:  startStop.GetRemoteAddress(),
		UserAgent:      startStop.GetUserAgent(),
		StatusCode:     startStop.GetStatusCode(),
		ContentLength:  startStop.GetContentLength(),
		ApplicationId:  uuidToHex(startStop.GetApplicationId()),
		InstanceIndex:  startStop.GetInstanceIndex(),
		Forwarded:      startStop.GetForwarded(),
	}

	splunkEvent := buildSplunkMetric(nozzleEvent, &splunkHttpStartStopMetric.CommonMetricFields)
	splunkEvent.Event = splunkHttpStartStopMetric
	return splunkEvent
}

type SplunkLogMessageMetric struct {
	CommonMetricFields
	Message        string `json:"logMessage"`
	MessageType    string `json:"MessageType"`
	Timestamp      int64  `json:"timestamp"`
	AppId          string `json:"appId"`
	SourceType     string `json:"sourceType"`
	SourceInstance string `json:"sourceInstance"`
}

func BuildLogMessageMetric(nozzleEvent *events.Envelope) *SplunkEvent {
	logMessageMetric := nozzleEvent.LogMessage
	splunkLogMessageMetric := SplunkLogMessageMetric{
		Message:        string(logMessageMetric.GetMessage()),
		MessageType:    logMessageMetric.GetMessageType().String(),
		Timestamp:      logMessageMetric.GetTimestamp(),
		AppId:          logMessageMetric.GetAppId(),
		SourceType:     logMessageMetric.GetSourceType(),
		SourceInstance: logMessageMetric.GetSourceInstance(),
	}

	splunkEvent := buildSplunkMetric(nozzleEvent, &splunkLogMessageMetric.CommonMetricFields)
	splunkEvent.Event = splunkLogMessageMetric
	return splunkEvent
}

type SplunkValueMetric struct {
	CommonMetricFields
	Name  string  `json:"name"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

func BuildValueMetric(nozzleEvent *events.Envelope) *SplunkEvent {
	valueMetric := nozzleEvent.ValueMetric
	splunkValueMetric := SplunkValueMetric{
		Name:  valueMetric.GetName(),
		Value: valueMetric.GetValue(),
		Unit:  valueMetric.GetUnit(),
	}

	splunkEvent := buildSplunkMetric(nozzleEvent, &splunkValueMetric.CommonMetricFields)
	splunkEvent.Event = splunkValueMetric
	return splunkEvent
}

type SplunkCounterEventMetric struct {
	CommonMetricFields
	Name  string `json:"name"`
	Delta uint64 `json:"delta"`
	Total uint64 `json:"total"`
}

func BuildCounterEventMetric(nozzleEvent *events.Envelope) *SplunkEvent {
	counterEvent := nozzleEvent.GetCounterEvent()
	splunkCounterEventMetric := SplunkCounterEventMetric{
		Name:  counterEvent.GetName(),
		Delta: counterEvent.GetDelta(),
		Total: counterEvent.GetTotal(),
	}

	splunkEvent := buildSplunkMetric(nozzleEvent, &splunkCounterEventMetric.CommonMetricFields)
	splunkEvent.Event = splunkCounterEventMetric
	return splunkEvent
}

type SplunkErrorMetric struct {
	CommonMetricFields
	Source  string `json:"source"`
	Code    int32  `json:"code"`
	Message string `json:"message"`
}

func BuildErrorMetric(nozzleEvent *events.Envelope) *SplunkEvent {
	errorMetric := nozzleEvent.Error
	splunkErrorMetric := SplunkErrorMetric{
		Source:  errorMetric.GetSource(),
		Code:    errorMetric.GetCode(),
		Message: errorMetric.GetMessage(),
	}

	splunkEvent := buildSplunkMetric(nozzleEvent, &splunkErrorMetric.CommonMetricFields)
	splunkEvent.Event = splunkErrorMetric
	return splunkEvent
}

type SplunkContainerMetric struct {
	CommonMetricFields
	ApplicationId string  `json:"applicationId"`
	InstanceIndex int32   `json:"instanceIndex"`
	CpuPercentage float64 `json:"cpuPercentage"`
	MemoryBytes   uint64  `json:"memoryBytes"`
	DiskBytes     uint64  `json:"diskBytes"`
}

func BuildContainerMetric(nozzleEvent *events.Envelope) *SplunkEvent {
	containerMetric := nozzleEvent.GetContainerMetric()
	splunkContainerMetric := SplunkContainerMetric{
		ApplicationId: containerMetric.GetApplicationId(),
		InstanceIndex: containerMetric.GetInstanceIndex(),
		CpuPercentage: containerMetric.GetCpuPercentage(),
		MemoryBytes:   containerMetric.GetMemoryBytes(),
		DiskBytes:     containerMetric.GetDiskBytes(),
	}

	splunkEvent := buildSplunkMetric(nozzleEvent, &splunkContainerMetric.CommonMetricFields)
	splunkEvent.Event = splunkContainerMetric
	return splunkEvent
}

func nanoSecondsToSeconds(nanoseconds int64) string {
	seconds := float64(nanoseconds) * math.Pow(1000, -3)
	return fmt.Sprintf("%.3f", seconds)
}

const dashByte byte = '-'

func uuidToHex(uuid *events.UUID) string {
	if uuid == nil {
		return ""
	}

	buffer := bytes.NewBuffer(make([]byte, 0, 16))
	binary.Write(buffer, binary.LittleEndian, uuid.Low)
	binary.Write(buffer, binary.LittleEndian, uuid.High)
	bufferBytes := buffer.Bytes()

	hexBuffer := make([]byte, 36)
	hex.Encode(hexBuffer[0:8], bufferBytes[0:4])
	hexBuffer[8] = dashByte
	hex.Encode(hexBuffer[9:13], bufferBytes[4:6])
	hexBuffer[13] = dashByte
	hex.Encode(hexBuffer[14:18], bufferBytes[6:8])
	hexBuffer[18] = dashByte
	hex.Encode(hexBuffer[19:23], bufferBytes[8:10])
	hexBuffer[23] = dashByte
	hex.Encode(hexBuffer[24:], bufferBytes[10:])

	return string(hexBuffer)
}
