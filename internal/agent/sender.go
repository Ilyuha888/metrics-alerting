package agent

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/Ilyuha888/metrics-alerting/internal/models"
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
	var errs []error

	for name, v := range snap.Gauges {
		value := float64(v)
		if err := s.post(models.Metrics{ID: name, MType: metrics.KindGauge, Value: &value}); err != nil {
			errs = append(errs, err)
		}
	}
	delta := int64(snap.PollCount)
	if err := s.post(models.Metrics{ID: "PollCount", MType: metrics.KindCounter, Delta: &delta}); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("%d of %d metrics failed, first: %w", len(errs), len(snap.Gauges)+1, errs[0])
	}
	return nil
}

func (s *Sender) post(m models.Metrics) error {
	buf, err := gzipJSON(m)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/update", s.endpoint)
	req, err := http.NewRequest(http.MethodPost, url, buf)
	if err != nil {
		return fmt.Errorf("build request %s: %w", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")

	// The client's error already names the method and the URL.
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", m.ID, err)
	}
	defer resp.Body.Close()

	// Draining returns the connection to the pool for the next metric.
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return fmt.Errorf("drain %s for %s: %w", url, m.ID, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("post %s for %s: status %d", url, m.ID, resp.StatusCode)
	}
	return nil
}

func gzipJSON(m models.Metrics) (*bytes.Buffer, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(m); err != nil {
		return nil, fmt.Errorf("encode %s: %w", m.ID, err)
	}
	// Close writes the gzip footer; without it the server reads a truncated stream.
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("compress %s: %w", m.ID, err)
	}
	return &buf, nil
}
