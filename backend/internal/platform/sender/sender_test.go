package sender

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWebhookPostsJSONWithBearerKey(t *testing.T) {
	var got map[string]string
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	w := &Webhook{URL: srv.URL, APIKey: "k", From: "TUMAINI"}
	require.NoError(t, w.Send(context.Background(), Message{To: "255700000001", Subject: "Code", Body: "123456"}))
	require.Equal(t, "Bearer k", auth)
	require.Equal(t, "255700000001", got["to"])
	require.Equal(t, "Code: 123456", got["message"])
}

func TestWebhookReportsGatewayErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	require.Error(t, (&Webhook{URL: srv.URL}).Send(context.Background(), Message{To: "1"}))
}

func TestSMTPRejectsHeaderInjection(t *testing.T) {
	s := &SMTP{Host: "127.0.0.1", Port: 1, From: "a@b.c"}
	require.ErrorContains(t, s.Send(context.Background(), Message{To: "x@y.z\r\nBcc: evil@z", Subject: "s"}), "injection")
}
