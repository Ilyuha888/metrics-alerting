package agent

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
)

const sendTimeout = 5 * time.Second

type Sender struct {
	endpoint string
	client   *http.Client
}

func NewSender(endpoint string) *Sender {
	return &Sender{
		endpoint: strings.TrimSuffix(endpoint, "/"),
		client:   &http.Client{Timeout: sendTimeout},
	}
}

// Send tries every metric even after a failure, so one hiccup does not drop the batch.
func (s *Sender) Send(snap Snapshot) error {
	var firstErr error
	failed := 0
	note := func(err error) {
		failed++
		if firstErr == nil {
			firstErr = err
		}
	}

	for name, v := range snap.Gauges {
		value := strconv.FormatFloat(float64(v), 'f', -1, 64)
		if err := s.post(metrics.KindGauge, name, value); err != nil {
			note(err)
		}
	}
	value := strconv.FormatInt(int64(snap.PollCount), 10)
	if err := s.post(metrics.KindCounter, "PollCount", value); err != nil {
		note(err)
	}

	if failed > 0 {
		return fmt.Errorf("%d of %d metrics failed, first: %w", failed, len(snap.Gauges)+1, firstErr)
	}
	return nil
}

func (s *Sender) post(kind metrics.Kind, name, value string) error {
	url := fmt.Sprintf("%s/update/%s/%s/%s", s.endpoint, kind, name, value)

	req, err := http.NewRequest(http.MethodPost, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("build request %s: %w", url, err)
	}
	req.Header.Set("Content-Type", "text/plain")

	// The client's error already names the method and the URL.
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Draining returns the connection to the pool for the next metric.
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return fmt.Errorf("drain %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("post %s: status %d", url, resp.StatusCode)
	}
	return nil
}
