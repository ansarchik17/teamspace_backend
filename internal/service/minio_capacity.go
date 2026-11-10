package service

import (
	"bufio"
	"context"
	"errors"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type MinIOCapacity struct {
	endpoint, token string
	reservePercent  int64
	client          *http.Client
}

func NewMinIOCapacity(cfg config.MinIOConfig) (*MinIOCapacity, error) {
	scheme := "http"
	if cfg.UseSSL {
		scheme = "https"
	}
	endpoint := cfg.Endpoint
	if !strings.Contains(endpoint, "://") {
		endpoint = scheme + "://" + endpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("invalid MinIO capacity endpoint")
	}
	if cfg.CapacityReservePercent < 0 || cfg.CapacityReservePercent >= 100 {
		return nil, errors.New("MinIO capacity reserve must be between 0 and 99")
	}
	return &MinIOCapacity{endpoint: strings.TrimRight(endpoint, "/") + "/minio/v2/metrics/cluster", token: cfg.MetricsToken, reservePercent: cfg.CapacityReservePercent, client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (m *MinIOCapacity) Capacity(ctx context.Context) (domain.StorageCapacity, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.endpoint, nil)
	if err != nil {
		return domain.StorageCapacity{}, errors.New("storage capacity unavailable")
	}
	if m.token != "" {
		req.Header.Set("Authorization", "Bearer "+m.token)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return domain.StorageCapacity{}, errors.New("storage capacity unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return domain.StorageCapacity{}, errors.New("storage capacity unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if err != nil || len(data) > 4*1024*1024 {
		return domain.StorageCapacity{}, errors.New("invalid storage capacity response")
	}
	total, free, err := parseCapacity(string(data))
	if err != nil {
		return domain.StorageCapacity{}, err
	}
	// Divide first to avoid overflow, round the reserved portion up.
	reserve := total/100*m.reservePercent + (total%100*m.reservePercent+99)/100
	return domain.StorageCapacity{TotalBytes: total, FreeBytes: free, ReserveBytes: reserve, ObservedAt: time.Now().UTC()}, nil
}
func parseCapacity(data string) (int64, int64, error) {
	values := map[string]int64{}
	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.SplitN(fields[0], "{", 2)[0]
		if name != "minio_cluster_capacity_usable_total_bytes" && name != "minio_cluster_capacity_usable_free_bytes" {
			continue
		}
		if _, exists := values[name]; exists {
			return 0, 0, errors.New("ambiguous storage capacity")
		}
		v, err := strconv.ParseFloat(fields[1], 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v >= float64(1<<63) || math.Trunc(v) != v {
			return 0, 0, errors.New("invalid storage capacity")
		}
		values[name] = int64(v)
	}
	total, tok := values["minio_cluster_capacity_usable_total_bytes"]
	free, fok := values["minio_cluster_capacity_usable_free_bytes"]
	if scanner.Err() != nil || !tok || !fok || total <= 0 || free > total {
		return 0, 0, errors.New("missing or invalid storage capacity")
	}
	return total, free, nil
}
