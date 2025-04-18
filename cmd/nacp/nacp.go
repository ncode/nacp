package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/mxab/nacp/admissionctrl/types"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-multierror"
	"github.com/hashicorp/nomad/api"
	"github.com/hashicorp/nomad/helper"
	"github.com/mxab/nacp/admissionctrl"
	"github.com/mxab/nacp/admissionctrl/mutator"
	"github.com/mxab/nacp/admissionctrl/notation"
	"github.com/mxab/nacp/admissionctrl/validator"
	"github.com/mxab/nacp/config"
	"github.com/notaryproject/notation-go/dir"
	"github.com/notaryproject/notation-go/verifier/truststore"
)

type contextKeyWarnings struct{}
type contextKeyValidationError struct{}

var (
	ctxWarnings        = contextKeyWarnings{}
	ctxValidationError = contextKeyValidationError{}
	jobPathRegex       = regexp.MustCompile(`^/v1/job/[a-zA-Z]+[a-z-Z0-9\-]*$`)
	jobPlanPathRegex   = regexp.MustCompile(`^/v1/job/[a-zA-Z]+[a-z-Z0-9\-]*/plan$`)

	nomadTimeout = 310 * time.Second
)

// New function to get client IP
func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header first
	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded != "" {
		return strings.Split(forwarded, ",")[0]
	}

	// Fall back to RemoteAddr
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	return ip
}

func resolveTokenAccessor(transport http.RoundTripper, nomadAddress *url.URL, token string) (*api.ACLToken, error) {
	if token == "" {
		return nil, nil
	}

	client := &http.Client{
		Transport: transport,
	}
	if transport == nil {
		client = http.DefaultClient
	}

	selfURL := *nomadAddress
	selfURL.Path = "/v1/acl/token/self"

	req, err := http.NewRequest("GET", selfURL.String(), nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-Nomad-Token", token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %s", resp.Status)
	}

	var aclToken api.ACLToken
	if err := json.NewDecoder(resp.Body).Decode(&aclToken); err != nil {
		return nil, err
	}

	return &aclToken, nil
}
func NewProxyHandler(nomadAddress *url.URL, jobHandler *admissionctrl.JobHandler, appLogger hclog.Logger, transport *http.Transport) func(http.ResponseWriter, *http.Request) {

	proxy := httputil.NewSingleHostReverseProxy(nomadAddress)
	if transport != nil {
		proxy.Transport = transport
	}

	originalDirector := proxy.Director

	proxy.Director = func(r *http.Request) {
		originalDirector(r)
	}

	proxy.ModifyResponse = func(resp *http.Response) error {

		var err error

		if isRegister(resp.Request) {
			err = handRegisterResponse(resp, appLogger)
		} else if isPlan(resp.Request) {
			err = handleJobPlanResponse(resp, appLogger)
		} else if isValidate(resp.Request) {
			err = handleJobValdidateResponse(resp, appLogger)
		}
		if err != nil {
			appLogger.Error("Preparing response failed", "error", err)
			return err
		}

		return nil
	}

	return func(w http.ResponseWriter, r *http.Request) {

		ctx := r.Context()
		reqCtx := &config.RequestContext{
			ClientIP: getClientIP(r),
		}

		token := r.Header.Get("X-Nomad-Token")
		if jobHandler.ResolveToken() {
			tokenInfo, err := resolveTokenAccessor(transport, nomadAddress, token)
			if err != nil {
				appLogger.Error("Resolving token failed", "error", err)
			}
			if tokenInfo != nil {
				reqCtx.AccessorID = tokenInfo.AccessorID
				reqCtx.TokenInfo = tokenInfo
			}
		}

		// Even tho we have resolveToken set to true, the initial connection will be issued without a token for the auth
		// so it's better to validate whether it's populated or not
		if reqCtx.TokenInfo != nil {
			appLogger.Info("Request received", "path", r.URL.Path, "method", r.Method, "clientIP", reqCtx.ClientIP, "accessorID", reqCtx.AccessorID)
		} else {
			appLogger.Info("Request received", "path", r.URL.Path, "method", r.Method, "clientIP", reqCtx.ClientIP)
		}

		// Store context
		ctx = context.WithValue(ctx, "request_context", reqCtx)
		r = r.WithContext(ctx)

		var err error
		if isRegister(r) {
			r, err = handleRegister(r, appLogger, jobHandler)

		} else if isPlan(r) {
			r, err = handlePlan(r, appLogger, jobHandler)

		} else if isValidate(r) {
			r, err = handleValidate(r, appLogger, jobHandler)

		}
		if err != nil {
			appLogger.Warn("Error applying admission controllers", "error", err)
			writeError(w, err)

		} else {
			proxy.ServeHTTP(w, r)
		}

	}

}

func handRegisterResponse(resp *http.Response, appLogger hclog.Logger) error {

	warnings, ok := resp.Request.Context().Value(ctxWarnings).([]error)
	if !ok && len(warnings) == 0 {
		return nil
	}

	response := &api.JobRegisterResponse{}
	reader := resp.Body

	isGzip, reader, err := checkIfGzipAndTransformReader(resp, reader)
	if err != nil {
		return err
	}
	defer reader.Close()
	if err := json.NewDecoder(reader).Decode(response); err != nil {
		return err
	}

	response.Warnings = buildFullWarningMsg(response.Warnings, warnings)

	responeData, err := json.Marshal(response)

	if err != nil {
		return err
	}

	if isGzip {
		rewriteResponseGzip(resp, responeData)
	} else {
		rewriteResponse(resp, responeData)
	}

	return nil
}

func checkIfGzipAndTransformReader(resp *http.Response, reader io.ReadCloser) (bool, io.ReadCloser, error) {
	enc := resp.Header.Get("Content-Encoding")
	isGzip := enc == "gzip"
	if isGzip {
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return false, nil, err
		}

		reader = gzipReader
	}
	return isGzip, reader, nil
}
func handleJobPlanResponse(resp *http.Response, appLogger hclog.Logger) error {
	warnings, ok := resp.Request.Context().Value(ctxWarnings).([]error)
	if !ok && len(warnings) == 0 {
		return nil
	}

	isGzip, reader, err := checkIfGzipAndTransformReader(resp, resp.Body)
	if err != nil {
		return err
	}
	defer reader.Close()

	response := &api.JobPlanResponse{}
	if err := json.NewDecoder(reader).Decode(response); err != nil {
		return err
	}

	response.Warnings = buildFullWarningMsg(response.Warnings, warnings)

	responeData, err := json.Marshal(response)

	if err != nil {
		return err
	}

	if isGzip {
		rewriteResponseGzip(resp, responeData)
	} else {
		rewriteResponse(resp, responeData)
	}
	return nil
}
func handleJobValdidateResponse(resp *http.Response, appLogger hclog.Logger) error {

	ctx := resp.Request.Context()
	validationErr, okErr := ctx.Value(ctxValidationError).(error)
	warnings, okWarnings := resp.Request.Context().Value(ctxWarnings).([]error)
	if !okErr && !okWarnings {
		return nil
	}

	response := &api.JobValidateResponse{}
	isGzip, reader, err := checkIfGzipAndTransformReader(resp, resp.Body)
	if err != nil {
		return err
	}
	defer reader.Close()

	if err := json.NewDecoder(reader).Decode(response); err != nil {
		return err
	}

	if validationErr != nil {
		validationErrors := []string{}
		var validationError string
		if merr, ok := validationErr.(*multierror.Error); ok {
			for _, err := range merr.Errors {
				validationErrors = append(validationErrors, err.Error())
			}
			validationError = merr.Error()
		} else { // This should never happen, but just in case
			validationErrors = append(validationErrors, validationErr.Error())
			validationError = err.Error()
		}

		response.ValidationErrors = validationErrors
		response.Error = validationError
	}

	if len(warnings) > 0 {
		response.Warnings = buildFullWarningMsg(response.Warnings, warnings)
	}

	responeData, err := json.Marshal(response)

	if err != nil {
		appLogger.Error("Error marshalling job", "error", err)
		return err
	}

	if isGzip {
		rewriteResponseGzip(resp, responeData)
	} else {
		rewriteResponse(resp, responeData)
	}

	return nil
}

func buildFullWarningMsg(upstreamResponseWarnings string, warnings []error) string {
	allWarnings := &multierror.Error{}

	if upstreamResponseWarnings != "" {
		multierror.Append(allWarnings, fmt.Errorf("%s", upstreamResponseWarnings))
	}
	allWarnings = multierror.Append(allWarnings, warnings...)
	warningMsg := helper.MergeMultierrorWarnings(allWarnings)
	return warningMsg
}

func rewriteResponse(resp *http.Response, newResponeData []byte) {
	resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(newResponeData)))

	resp.ContentLength = int64(len(newResponeData))
	resp.Body = io.NopCloser(bytes.NewBuffer(newResponeData))
}
func rewriteResponseGzip(resp *http.Response, newResponeData []byte) {

	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	gz.Write(newResponeData)
	gz.Close()

	resp.Header.Set("Content-Length", strconv.Itoa(compressed.Len()))
	resp.ContentLength = int64(compressed.Len())

	resp.Body = io.NopCloser(&compressed)
}
func rewriteRequest(r *http.Request, data []byte) {

	r.ContentLength = int64(len(data))
	r.Body = io.NopCloser(bytes.NewBuffer(data))
}

func handleRegister(r *http.Request, appLogger hclog.Logger, jobHandler *admissionctrl.JobHandler) (*http.Request, error) {
	body := r.Body
	jobRegisterRequest := &api.JobRegisterRequest{}

	if err := json.NewDecoder(body).Decode(jobRegisterRequest); err != nil {

		return r, fmt.Errorf("failed decoding job, skipping admission controller: %w", err)
	}
	orginalJob := jobRegisterRequest.Job
	payload := &types.Payload{
		Job: orginalJob,
	}

	if reqCtx, ok := r.Context().Value("request_context").(*config.RequestContext); ok {
		payload.Context = reqCtx
	}

	job, warnings, err := jobHandler.ApplyAdmissionControllers(payload)
	if err != nil {
		return r, fmt.Errorf("admission controllers send an error, returning error: %w", err)
	}
	jobRegisterRequest.Job = job

	data, err := json.Marshal(jobRegisterRequest)

	if err != nil {
		return r, fmt.Errorf("error marshalling job: %w", err)
	}

	ctx := r.Context()
	if len(warnings) > 0 {
		ctx = context.WithValue(ctx, ctxWarnings, warnings)
	}

	appLogger.Debug("Job after admission controllers", "job", string(data))
	r = r.WithContext(ctx)
	rewriteRequest(r, data)
	return r, nil
}
func handlePlan(r *http.Request, appLogger hclog.Logger, jobHandler *admissionctrl.JobHandler) (*http.Request, error) {
	body := r.Body
	jobPlanRequest := &api.JobPlanRequest{}

	if err := json.NewDecoder(body).Decode(jobPlanRequest); err != nil {
		return r, fmt.Errorf("failed decoding job, skipping admission controller: %w", err)
	}
	orginalJob := jobPlanRequest.Job
	payload := &types.Payload{
		Job: orginalJob,
	}

	if reqCtx, ok := r.Context().Value("request_context").(*config.RequestContext); ok {
		payload.Context = reqCtx
	}

	job, warnings, err := jobHandler.ApplyAdmissionControllers(payload)
	if err != nil {
		return r, fmt.Errorf("admission controllers send an error, returning error: %w", err)
	}

	jobPlanRequest.Job = job

	data, err := json.Marshal(jobPlanRequest)

	if err != nil {
		return r, fmt.Errorf("error marshalling job: %w", err)
	}
	ctx := r.Context()
	if len(warnings) > 0 {
		ctx = context.WithValue(ctx, ctxWarnings, warnings)

	}
	r = r.WithContext(ctx)
	appLogger.Debug("Job after admission controllers", "job", string(data))
	rewriteRequest(r, data)
	return r, nil
}

func handleValidate(r *http.Request, appLogger hclog.Logger, jobHandler *admissionctrl.JobHandler) (*http.Request, error) {

	body := r.Body
	jobValidateRequest := &api.JobValidateRequest{}
	err := json.NewDecoder(body).Decode(jobValidateRequest)
	if err != nil {
		return r, err
	}
	job := jobValidateRequest.Job
	payload := &types.Payload{
		Job: job,
	}

	if reqCtx, ok := r.Context().Value("request_context").(*config.RequestContext); ok {
		payload.Context = reqCtx
	}

	job, mutateWarnings, err := jobHandler.AdmissionMutators(payload)
	if err != nil {
		return r, err
	}
	jobValidateRequest.Job = job
	payload.Job = job

	validateWarnings, err := jobHandler.AdmissionValidators(payload)
	//copied from https: //github.com/hashicorp/nomad/blob/v1.5.0/nomad/job_endpoint.go#L574

	ctx := r.Context()
	ctx = context.WithValue(ctx, ctxValidationError, err)

	validateWarnings = append(validateWarnings, mutateWarnings...)

	data, err := json.Marshal(jobValidateRequest)
	if err != nil {
		return r, err
	}

	if len(validateWarnings) > 0 {
		ctx = context.WithValue(ctx, ctxWarnings, validateWarnings)

	}
	r = r.WithContext(ctx)
	rewriteRequest(r, data)
	return r, nil

}

func writeError(w http.ResponseWriter, err error) {
	w.WriteHeader(http.StatusInternalServerError)
	w.Write([]byte(err.Error()))
}
func isRegister(r *http.Request) bool {
	isRegister := isCreate(r) || isUpdate(r)
	return isRegister
}

// cli does PUT, browser does POST :/
func isCreate(r *http.Request) bool {
	return (r.Method == "PUT" || r.Method == "POST") && r.URL.Path == "/v1/jobs"
}
func isUpdate(r *http.Request) bool {

	return (r.Method == "PUT" || r.Method == "POST") && jobPathRegex.MatchString(r.URL.Path)
}
func isPlan(r *http.Request) bool {

	return (r.Method == "PUT" || r.Method == "POST") && jobPlanPathRegex.MatchString(r.URL.Path)
}
func isValidate(r *http.Request) bool {

	return (r.Method == "PUT" || r.Method == "POST") && r.URL.Path == "/v1/validate/job"
}

// https://www.codedodle.com/go-reverse-proxy-example.html
// https://joshsoftware.wordpress.com/2021/05/25/simple-and-powerful-reverseproxy-in-go/
func main() {

	appLogger := hclog.New(&hclog.LoggerOptions{
		Name:   "nacp",
		Level:  hclog.LevelFromString("DEBUG"),
		Output: os.Stdout,
	})

	c := buildConfig(appLogger)
	appLogger.SetLevel(hclog.LevelFromString(c.LogLevel))
	server, err := buildServer(c, appLogger)

	if err != nil {
		appLogger.Error("Failed to build server", "error", err)
		os.Exit(1)
	}

	var end error
	if c.Tls != nil {
		appLogger.Info("Starting NACP with TLS", "bind", c.Bind, "port", c.Port)
		end = server.ListenAndServeTLS(c.Tls.CertFile, c.Tls.KeyFile)
	} else {
		appLogger.Info("Starting NACP", "bind", c.Bind, "port", c.Port)
		end = server.ListenAndServe()
	}
	appLogger.Error("NACP stopped", "error", end)
}

func buildServer(c *config.Config, appLogger hclog.Logger) (*http.Server, error) {
	backend, err := url.Parse(c.Nomad.Address)
	if err != nil {
		return nil, fmt.Errorf("failed to parse nomad address: %w", err)
	}
	proxyTransport := http.DefaultTransport.(*http.Transport).Clone()
	proxyTransport.DialContext = (&net.Dialer{
		Timeout:   nomadTimeout,
		KeepAlive: nomadTimeout,
	}).DialContext
	proxyTransport.TLSHandshakeTimeout = nomadTimeout

	if c.Nomad.TLS != nil {
		nomadTlsConfig, err := buildTlsConfig(*c.Nomad.TLS)
		if err != nil {
			return nil, fmt.Errorf("failed to create custom transport: %w", err)

		}
		proxyTransport.TLSClientConfig = nomadTlsConfig
	}

	jobMutators, resolveTokenMutators, err := createMutators(c, appLogger.Named("mutators"))
	if err != nil {
		return nil, fmt.Errorf("failed to create mutators: %w", err)
	}

	jobValidators, resolveTokenValidators, err := createValidators(c, appLogger.Named("validators"))
	if err != nil {
		return nil, fmt.Errorf("failed to create validators: %w", err)
	}

	var resolveToken bool
	if resolveTokenMutators || resolveTokenValidators {
		resolveToken = true
	}

	handler := admissionctrl.NewJobHandler(

		jobMutators,
		jobValidators,
		appLogger.Named("handler"),
		resolveToken,
	)

	proxy := NewProxyHandler(backend, handler, appLogger, proxyTransport)

	bind := fmt.Sprintf("%s:%d", c.Bind, c.Port)
	var tlsConfig *tls.Config

	if c.Tls != nil && c.Tls.CaFile != "" {
		tlsConfig, err = createTlsConfig(c.Tls.CaFile, c.Tls.NoClientCert)
		if err != nil {
			return nil, fmt.Errorf("failed to create tls config: %w", err)

		}
	}

	server := &http.Server{
		Addr:         bind,
		TLSConfig:    tlsConfig,
		Handler:      http.HandlerFunc(proxy),
		ReadTimeout:  nomadTimeout,
		WriteTimeout: nomadTimeout,
	}
	return server, nil
}

func buildConfig(logger hclog.Logger) *config.Config {

	configPtr := flag.String("config", "", "point to a nacp config file")
	flag.Parse()
	var c *config.Config

	if _, err := os.Stat(*configPtr); err == nil && *configPtr != "" {
		c, err = config.LoadConfig(*configPtr)
		if err != nil {
			logger.Error("Failed to load config", "error", err)
			os.Exit(1)
		}
		logger.Info("Loaded config", "config", *configPtr)
	} else {
		logger.Info("No config file found, using default config")
		c = config.DefaultConfig()
	}
	return c
}

func createTlsConfig(caFile string, noClientCert bool) (*tls.Config, error) {
	caCert, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)
	clientAuth := tls.RequireAndVerifyClientCert
	if noClientCert {
		clientAuth = tls.NoClientCert
	}
	tlsConfig := &tls.Config{
		ClientCAs:  caCertPool,
		ClientAuth: clientAuth,
	}

	return tlsConfig, nil
}

func createMutators(c *config.Config, logger hclog.Logger) ([]admissionctrl.JobMutator, bool, error) {
	var jobMutators []admissionctrl.JobMutator
	var resolveToken bool
	for _, m := range c.Mutators {
		if m.ResolveToken {
			resolveToken = true
		}
		switch m.Type {
		case "opa_json_patch":
			notationVerifier, err := buildVerifierIfEnabled(m.OpaRule.Notation, logger.Named("notation_verifier"))
			if err != nil {
				return nil, resolveToken, err
			}
			mutator, err := mutator.NewOpaJsonPatchMutator(m.Name, m.OpaRule.Filename, m.OpaRule.Query, logger.Named("opa_mutator"), notationVerifier)
			if err != nil {
				return nil, resolveToken, err
			}
			jobMutators = append(jobMutators, mutator)

		case "json_patch_webhook":
			mutator, err := mutator.NewJsonPatchWebhookMutator(m.Name, m.Webhook.Endpoint, m.Webhook.Method, logger.Named("json_patch_webhook_mutator"))
			if err != nil {
				return nil, resolveToken, err
			}
			jobMutators = append(jobMutators, mutator)

		default:
			return nil, resolveToken, fmt.Errorf("unknown mutator type %s", m.Type)
		}

	}
	return jobMutators, resolveToken, nil
}
func createValidators(c *config.Config, logger hclog.Logger) ([]admissionctrl.JobValidator, bool, error) {
	var jobValidators []admissionctrl.JobValidator
	var resolveToken bool
	for _, v := range c.Validators {
		if v.ResolveToken {
			resolveToken = true
		}
		switch v.Type {
		case "opa":
			notationVerifier, err := buildVerifierIfEnabled(v.Notation, logger.Named("notation_verifier"))
			if err != nil {
				return nil, resolveToken, err
			}
			opaValidator, err := validator.NewOpaValidator(v.Name, v.OpaRule.Filename, v.OpaRule.Query, logger.Named("opa_validator"), notationVerifier)
			if err != nil {
				return nil, resolveToken, err
			}
			jobValidators = append(jobValidators, opaValidator)

		case "webhook":
			validator, err := validator.NewWebhookValidator(v.Name, v.Webhook.Endpoint, v.Webhook.Method, logger.Named("webhook_validator"))
			if err != nil {
				return nil, resolveToken, err
			}
			jobValidators = append(jobValidators, validator)
		case "notation":
			notationVerifier, err := buildVerifier(v.Notation, logger.Named("notation_verifier"))
			if err != nil {
				return nil, resolveToken, err
			}
			validator := validator.NewNotationValidator(logger.Named("notation_validator"), v.Name, notationVerifier)

			jobValidators = append(jobValidators, validator)

		default:
			return nil, resolveToken, fmt.Errorf("unknown validator type %s", v.Type)
		}

	}
	return jobValidators, resolveToken, nil
}
func buildVerifierIfEnabled(notationVerifierConfig *config.NotationVerifierConfig, logger hclog.Logger) (notation.ImageVerifier, error) {
	if notationVerifierConfig == nil {
		return nil, nil
	}
	return buildVerifier(notationVerifierConfig, logger)
}
func buildVerifier(notationVerifierConfig *config.NotationVerifierConfig, logger hclog.Logger) (notation.ImageVerifier, error) {

	if notationVerifierConfig == nil {
		return nil, fmt.Errorf("notation verifier config is nil")
	}
	policy, err := notation.LoadTrustPolicyDocument(notationVerifierConfig.TrustPolicyFile)
	if err != nil {
		return nil, err
	}
	ts := truststore.NewX509TrustStore(dir.NewSysFS(notationVerifierConfig.TrustStoreDir))

	return notation.NewImageVerifier(policy, ts, notationVerifierConfig.RepoPlainHTTP, notationVerifierConfig.MaxSigAttempts, notationVerifierConfig.CredentialStoreFile, logger)
}

func buildTlsConfig(config config.NomadServerTLS) (*tls.Config, error) {
	// Create a custom transport to allow for self-signed certs
	// and to allow for a custom timeout

	//load key pair
	cert, err := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
	if err != nil {
		return nil, err
	}

	// create CA pool
	caCert, err := os.ReadFile(config.CaFile)
	if err != nil {
		return nil, err
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)

	tlsConfig := &tls.Config{
		InsecureSkipVerify: config.InsecureSkipVerify,

		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
	}
	return tlsConfig, err
}
