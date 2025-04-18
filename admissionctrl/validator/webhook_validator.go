package validator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/mxab/nacp/admissionctrl/types"
	"net/http"
	"net/url"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-multierror"
)

type WebhookValidator struct {
	endpoint *url.URL
	logger   hclog.Logger
	method   string
	name     string
}

type validationWebhookResponse struct {
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

func (w *WebhookValidator) Validate(payload *types.Payload) ([]error, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(w.method, w.endpoint.String(), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	// Add context headers and body if available
	if payload.Context != nil {
		// Add standard headers for backward compatibility
		if payload.Context.ClientIP != "" {
			req.Header.Set("X-Forwarded-For", payload.Context.ClientIP) // Standard proxy header
			req.Header.Set("NACP-Client-IP", payload.Context.ClientIP)  // NACP specific
		}
		if payload.Context.AccessorID != "" {
			req.Header.Set("NACP-Accessor-ID", payload.Context.AccessorID)
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	valdationResult := &validationWebhookResponse{}
	err = json.NewDecoder(resp.Body).Decode(valdationResult)

	if err != nil {
		return nil, err
	}

	if len(valdationResult.Errors) > 0 {
		w.logger.Error("validation errors", "errors", valdationResult.Errors, "rule", w.name, "job", payload.Job.ID)
		oneError := &multierror.Error{}
		for _, e := range valdationResult.Errors {
			oneError = multierror.Append(oneError, fmt.Errorf("%v", e))
		}
		return nil, oneError
	}

	var warnings []error
	if len(valdationResult.Warnings) > 0 {

		for _, w := range valdationResult.Warnings {
			warnings = append(warnings, fmt.Errorf("%v", w))
		}
		return warnings, nil

	}
	return warnings, nil
}
func (w *WebhookValidator) Name() string {
	return w.name
}
func NewWebhookValidator(name string, endpoint string, method string, logger hclog.Logger) (*WebhookValidator, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	return &WebhookValidator{
		name:     name,
		logger:   logger,
		endpoint: u,
		method:   method,
	}, nil
}
