package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nokia/srlinux-ndk-go/ndk"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/prototext"
)

type telemUpdate struct {
	op     string
	jsPath string
	jsData string
}

func (s *app) updateTelemetry(ctx context.Context, jsPath string, jsData string) {
	key := &ndk.TelemetryKey{JsPath: jsPath}
	data := &ndk.TelemetryData{JsonContent: jsData}
	info := &ndk.TelemetryInfo{Key: key, Data: data}
	telReq := &ndk.TelemetryUpdateRequest{
		State: []*ndk.TelemetryInfo{info},
	}
	log.Debugf("Updating telemetry with: %+v", telReq)
	b, err := prototext.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(telReq)
	if err != nil {
		log.Errorf("telemetry request Marshal failed: %+v", err)
		return
	}
	fmt.Printf("%s\n", string(b))
	r1, err := s.agent.TelemetryServiceClient.TelemetryAddOrUpdate(ctx, telReq)
	if err != nil {
		log.Errorf("Could not update telemetry key=%s: err=%v", jsPath, err)
		return
	}
	log.Debugf("Telemetry add/update status: %s, error_string: %q", r1.GetStatus().String(), r1.GetErrorStr())
}

func (s *app) deleteTelemetry(ctx context.Context, jsPath string) error {
	key := &ndk.TelemetryKey{JsPath: jsPath}
	telReq := &ndk.TelemetryDeleteRequest{}
	telReq.Key = make([]*ndk.TelemetryKey, 0)
	telReq.Key = append(telReq.Key, key)

	b, err := prototext.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(telReq)
	if err != nil {
		log.Errorf("telemetry request Marshal failed: %+v", err)
	}
	fmt.Printf("%s\n", string(b))

	r1, err := s.agent.TelemetryServiceClient.TelemetryDelete(ctx, telReq)
	if err != nil {
		log.Errorf("could not delete telemetry for key : %s", jsPath)
		return err
	}
	log.Debugf("telemetry delete status: %s, error_string: %q", r1.GetStatus().String(), r1.GetErrorStr())
	return nil
}

func (s *app) updateTelemetryCh(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case tu := <-s.tuCh:
			switch tu.op {
			case "update":
				s.updateTelemetry(ctx, tu.jsPath, tu.jsData)
			case "delete":
				s.deleteTelemetry(ctx, tu.jsPath)
			}
		}
	}
}

func updateTelemetryCh(ch chan *telemUpdate, p string, d any) {
	jsData, err := json.Marshal(d)
	if err != nil {
		log.Errorf("failed to marshal json data: %v", err)
		return
	}
	ch <- &telemUpdate{
		op:     "update",
		jsPath: p,
		jsData: string(jsData),
	}
}
