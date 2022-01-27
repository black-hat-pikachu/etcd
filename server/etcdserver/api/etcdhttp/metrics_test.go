package etcdhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/client/pkg/v3/testutil"
	"go.etcd.io/etcd/client/pkg/v3/types"
	"go.etcd.io/etcd/raft/v3"
	"go.etcd.io/etcd/server/v3/etcdserver"
	"go.uber.org/zap/zaptest"
)

type fakeStats struct{}

func (s *fakeStats) SelfStats() []byte   { return nil }
func (s *fakeStats) LeaderStats() []byte { return nil }
func (s *fakeStats) StoreStats() []byte  { return nil }

type fakeServerV2 struct {
	fakeServer
	health string
}

func (s *fakeServerV2) Leader() types.ID {
	if s.health == "true" {
		return 1
	}
	return types.ID(raft.None)
}
func (s *fakeServerV2) Do(ctx context.Context, r pb.Request) (etcdserver.Response, error) {
	if s.health == "true" {
		return etcdserver.Response{}, nil
	}
	return etcdserver.Response{}, fmt.Errorf("fail health check")
}
func (s *fakeServerV2) ClientCertAuthEnabled() bool { return false }

func TestHealthHandler(t *testing.T) {
	// define the input and expected output
	// input: alarms, and healthCheckURL
	tests := []struct {
		alarms         []*pb.AlarmMember
		healthCheckURL string
		statusCode     int
		health         string
	}{
		{
			alarms:         []*pb.AlarmMember{},
			healthCheckURL: "/health",
			statusCode:     http.StatusOK,
			health:         "true",
		},
		{
			alarms:         []*pb.AlarmMember{{MemberID: uint64(0), Alarm: pb.AlarmType_NOSPACE}},
			healthCheckURL: "/health",
			statusCode:     http.StatusServiceUnavailable,
			health:         "false",
		},
		{
			alarms:         []*pb.AlarmMember{{MemberID: uint64(0), Alarm: pb.AlarmType_NOSPACE}},
			healthCheckURL: "/health?exclude=NOSPACE",
			statusCode:     http.StatusOK,
			health:         "true",
		},
		{
			alarms:         []*pb.AlarmMember{},
			healthCheckURL: "/health?exclude=NOSPACE",
			statusCode:     http.StatusOK,
			health:         "true",
		},
		{
			alarms:         []*pb.AlarmMember{{MemberID: uint64(1), Alarm: pb.AlarmType_NOSPACE}, {MemberID: uint64(2), Alarm: pb.AlarmType_NOSPACE}, {MemberID: uint64(3), Alarm: pb.AlarmType_NOSPACE}},
			healthCheckURL: "/health?exclude=NOSPACE",
			statusCode:     http.StatusOK,
			health:         "true",
		},
		{
			alarms:         []*pb.AlarmMember{{MemberID: uint64(0), Alarm: pb.AlarmType_NOSPACE}, {MemberID: uint64(1), Alarm: pb.AlarmType_CORRUPT}},
			healthCheckURL: "/health?exclude=NOSPACE",
			statusCode:     http.StatusServiceUnavailable,
			health:         "false",
		},
		{
			alarms:         []*pb.AlarmMember{{MemberID: uint64(0), Alarm: pb.AlarmType_NOSPACE}, {MemberID: uint64(1), Alarm: pb.AlarmType_CORRUPT}},
			healthCheckURL: "/health?exclude=NOSPACE&exclude=CORRUPT",
			statusCode:     http.StatusOK,
			health:         "true",
		},
	}

	for i, tt := range tests {
		func() {
			mux := http.NewServeMux()
			HandleMetricsHealth(zaptest.NewLogger(t), mux, &fakeServerV2{
				fakeServer: fakeServer{alarms: tt.alarms},
				health:     tt.health,
			})
			ts := httptest.NewServer(mux)
			defer ts.Close()

			res, err := ts.Client().Do(&http.Request{Method: http.MethodGet, URL: testutil.MustNewURL(t, ts.URL+tt.healthCheckURL)})
			if err != nil {
				t.Errorf("fail serve http request %s %v in test case #%d", tt.healthCheckURL, err, i+1)
			}
			if res == nil {
				t.Errorf("got nil http response with http request %s in test case #%d", tt.healthCheckURL, i+1)
				return
			}
			if res.StatusCode != tt.statusCode {
				t.Errorf("want statusCode %d but got %d in test case #%d", tt.statusCode, res.StatusCode, i+1)
			}
			health, err := parseHealthOutput(res.Body)
			if err != nil {
				t.Errorf("fail parse health check output %v", err)
			}
			if health.Health != tt.health {
				t.Errorf("want health %s but got %s", tt.health, health.Health)
			}
		}()
	}
}

func parseHealthOutput(body io.Reader) (Health, error) {
	obj := Health{}
	d, derr := io.ReadAll(body)
	if derr != nil {
		return obj, derr
	}
	if err := json.Unmarshal(d, &obj); err != nil {
		return obj, err
	}
	return obj, nil
}
