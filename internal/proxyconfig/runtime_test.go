package proxyconfig

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPControllerExcludesBuiltInRoutesFromNodeCount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"all":["DIRECT","Hong Kong","Japan"],"now":"Hong Kong"}`))
	}))
	defer server.Close()
	controller := NewHTTPController(server.URL, time.Second)
	status, err := controller.GroupStatus(context.Background(), "PROXY")
	if err != nil || status.NodeCount != 2 || status.CurrentNode != "Hong Kong" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}
