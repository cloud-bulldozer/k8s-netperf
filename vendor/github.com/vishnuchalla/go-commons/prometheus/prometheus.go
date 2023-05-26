// Copyright 2020 The go-commons Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package prometheus

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	api "github.com/prometheus/client_golang/api"
	apiv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

// Used to intercept and passed custom auth headers to prometheus client request
func (bat authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if bat.username != "" {
		req.SetBasicAuth(bat.username, bat.password)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", bat.token))
	return bat.Transport.RoundTrip(req)
}

// NewClient creates a prometheus struct instance with the given parameters
func NewClient(url, token, username, password string, tlsVerify bool) (*Prometheus, error) {
	prometheus := Prometheus{
		Endpoint: url,
	}
	cfg := api.Config{
		Address: url,
		RoundTripper: authTransport{
			Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{InsecureSkipVerify: tlsVerify}},
			token:     token,
			username:  username,
			password:  password,
		},
	}
	c, err := api.NewClient(cfg)
	if err != nil {
		return &prometheus, err
	}
	prometheus.Api = apiv1.NewAPI(c)
	// Verify Prometheus connection prior returning
	if err := prometheus.verifyConnection(); err != nil {
		return &prometheus, err
	}
	return &prometheus, nil
}

// Query prometheus query wrapper
func (p *Prometheus) Query(query string, time time.Time) (model.Value, error) {
	var v model.Value
	v, _, err := p.Api.Query(context.TODO(), query, time)
	if err != nil {
		return v, err
	}
	return v, nil
}

// QueryRange prometheus queryRange wrapper
func (p *Prometheus) QueryRange(query string, start, end time.Time, step time.Duration) (model.Value, error) {
	var v model.Value
	r := apiv1.Range{Start: start, End: end, Step: step}
	v, _, err := p.Api.QueryRange(context.TODO(), query, r)
	if err != nil {
		return v, err
	}
	return v, nil
}

// Verifies prometheus connection
func (p *Prometheus) verifyConnection() error {
	_, err := p.Api.Runtimeinfo(context.TODO())
	if err != nil {
		return err
	}
	return nil
}
