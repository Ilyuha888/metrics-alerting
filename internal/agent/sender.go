package agent

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/Ilyuha888/metrics-alerting/internal/models"
	"github.com/Ilyuha888/metrics-alerting/internal/retry"
)

const sendTimeout = 5 * time.Second

type Sender struct {
	endpoint string
	client   *http.Client
	delays   []time.Duration
}

func NewSender(endpoint string) *Sender {
	return &Sender{
		endpoint: strings.TrimSuffix(endpoint, "/"),
		client:   &http.Client{Timeout: sendTimeout},
		delays:   retry.DefaultDelays,
	}
}

// Send makes one request, so a report lands whole or not at all. A snapshot with nothing in it
// sends nothing; any other includes PollCount, even at 0.
func (s *Sender) Send(snap Snapshot) error {
	if len(snap.Gauges) == 0 && snap.PollCount == 0 {
		return nil
	}

	list := make([]models.Metrics, 0, len(snap.Gauges)+1)
	for name, v := range snap.Gauges {
		value := float64(v)
		list = append(list, models.Metrics{ID: name, MType: metrics.KindGauge, Value: &value})
	}
	delta := int64(snap.PollCount)
	list = append(list, models.Metrics{ID: "PollCount", MType: metrics.KindCounter, Delta: &delta})

	return s.post(list)
}

func (s *Sender) post(list []models.Metrics) error {
	buf, err := gzipJSON(list)
	if err != nil {
		return err
	}
	body := buf.Bytes()

	return retry.Do(context.Background(), s.delays, isDialFailure, func() error {
		return s.postOnce(body)
	})
}

// isDialFailure accepts only a connection that never came up. After EOF, a timeout or a 5xx the
// request may have landed, and a repeat would count PollCount twice.
func isDialFailure(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}

func (s *Sender) postOnce(body []byte) error {
	url := fmt.Sprintf("%s/updates/", s.endpoint)
	// A reader per attempt: one that an earlier attempt read would send nothing on a repeat.
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request %s: %w", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")

	// The client's error already names the method and the URL.
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Close finishes reading only a small answer that arrives at once; after any other it drops the
	// connection. Draining returns the connection to the pool for the next report.
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return fmt.Errorf("drain %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("post %s: status %d", url, resp.StatusCode)
	}
	return nil
}

func gzipJSON(list []models.Metrics) (*bytes.Buffer, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(list); err != nil {
		return nil, fmt.Errorf("encode batch: %w", err)
	}
	// Close writes the gzip footer; without it the server reads a truncated stream.
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("compress batch: %w", err)
	}
	return &buf, nil
}
