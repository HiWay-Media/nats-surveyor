package surveyor

import (
	"bytes"
	"testing"
	"time"

	"github.com/nats-io/jsm.go"
	"github.com/nats-io/nats.go"
	ptu "github.com/prometheus/client_golang/prometheus/testutil"

	st "github.com/nats-io/nats-surveyor/test"
)

func TestJetStream_Load(t *testing.T) {
	js := st.NewJetStreamServer(t)
	defer js.Shutdown()

	opt := GetDefaultOptions()
	opt.URLs = js.ClientURL()

	obs, err := NewJetStreamAdvisoryListener("testdata/goodjs/global.json", *opt)
	if err != nil {
		t.Fatalf("jetstream load error: %s", err)
	}
	obs.Stop()

	_, err = NewJetStreamAdvisoryListener("testdata/badjs/missing.json", *opt)
	if err.Error() != "open testdata/badjs/missing.json: no such file or directory" {
		t.Fatalf("jetstream load error: %s", err)
	}

	_, err = NewJetStreamAdvisoryListener("testdata/badobs/bad.json", *opt)
	if err.Error() != "invalid JetStream advisory configuration: testdata/badobs/bad.json: name is required" {
		t.Fatalf("jetstream load error: %s", err)
	}
}

func TestJetStream_Handle(t *testing.T) {
	js := st.NewJetStreamServer(t)
	defer js.Shutdown()

	opt := GetDefaultOptions()
	opt.URLs = js.ClientURL()

	obs, err := NewJetStreamAdvisoryListener("testdata/goodjs/global.json", *opt)
	if err != nil {
		t.Fatalf("jetstream load error: %s", err)
	}
	defer obs.Stop()

	err = obs.Start()
	if err != nil {
		t.Fatalf("jetstream failed to start: %s", err)
	}

	nc, err := nats.Connect(js.ClientURL(), nats.UseOldRequestStyle())
	if err != nil {
		t.Fatalf("could not connect nats client: %s", err)
	}

	if known, _ := jsm.IsKnownStream("SURVEYOR"); known {
		t.Fatalf("SURVEYOR stream already exist")
	}

	str, err := jsm.NewStream("SURVEYOR", jsm.StreamConnection(jsm.WithConnection(nc)), jsm.Subjects("js.in.surveyor"), jsm.MemoryStorage())
	if err != nil {
		t.Fatalf("could not create stream: %s", err)
	}

	msg, err := nc.Request("js.in.surveyor", []byte("1"), time.Second)
	if err != nil {
		t.Fatalf("publish failed: %s", err)
	}
	if jsm.IsErrorResponse(msg) {
		t.Fatalf("publish failed: %s", string(msg.Data))
	}

	consumer, err := str.NewConsumer(jsm.AckWait(500*time.Millisecond), jsm.DurableName("OUT"), jsm.MaxDeliveryAttempts(1), jsm.SamplePercent(100))
	if err != nil {
		t.Fatalf("could not create consumer: %s", err)
	}

	consumer.NextMsg(jsm.WithTimeout(1100 * time.Millisecond))
	consumer.NextMsg(jsm.WithTimeout(1100 * time.Millisecond))

	msg, err = nc.Request("js.in.surveyor", []byte("2"), time.Second)
	if err != nil {
		t.Fatalf("publish failed: %s", err)
	}
	if jsm.IsErrorResponse(msg) {
		t.Fatalf("publish failed: %s", string(msg.Data))
	}

	msg, err = consumer.NextMsg(jsm.WithTimeout(time.Second))
	if err != nil {
		t.Fatalf("next failed: %s", err)
	}
	msg.Respond(nil)

	// time for advisories to be sent and handled
	time.Sleep(5 * time.Millisecond)

	expected := `
# HELP nats_survey_jetstream_delivery_exceeded_count Advisories about JetStream Consumer Delivery Exceeded events
# TYPE nats_survey_jetstream_delivery_exceeded_count counter
nats_survey_jetstream_delivery_exceeded_count{account="global",consumer="OUT",stream="SURVEYOR"} 1
`
	err = ptu.CollectAndCompare(jsDeliveryExceededCtr, bytes.NewReader([]byte(expected)))
	if err != nil {
		t.Fatalf("metrics failed: %s", err)
	}

	expected = `
# HELP nats_survey_jetstream_api_audit JetStream API access audit events
# TYPE nats_survey_jetstream_api_audit counter
nats_survey_jetstream_api_audit{account="global",server="jetstream",subject="$JS.API.CONSUMER.DURABLE.CREATE.SURVEYOR.OUT"} 1
nats_survey_jetstream_api_audit{account="global",server="jetstream",subject="$JS.API.CONSUMER.INFO.SURVEYOR.OUT"} 1
nats_survey_jetstream_api_audit{account="global",server="jetstream",subject="$JS.API.STREAM.CREATE.SURVEYOR"} 1
`
	err = ptu.CollectAndCompare(jsAPIAuditCtr, bytes.NewReader([]byte(expected)))
	if err != nil {
		t.Fatalf("metrics failed: %s", err)
	}

	expected = `
# HELP nats_survey_jetstream_acknowledgement_deliveries How many times messages took to be delivered and Acknowledged
# TYPE nats_survey_jetstream_acknowledgement_deliveries counter
nats_survey_jetstream_acknowledgement_deliveries{account="global",consumer="OUT",stream="SURVEYOR"} 1
`
	err = ptu.CollectAndCompare(jsAckMetricDeliveries, bytes.NewReader([]byte(expected)))
	if err != nil {
		t.Fatalf("metrics failed: %s", err)
	}
}
