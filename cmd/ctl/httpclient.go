package ctl

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	js "encoding/json"
	"fmt"
	"github.com/p9c/pod/pkg/log"
	"io/ioutil"
	"net"
	"net/http"

	"github.com/btcsuite/go-socks/socks"

	"github.com/p9c/pod/pkg/conte"
	"github.com/p9c/pod/pkg/pod"
	"github.com/p9c/pod/pkg/rpc/btcjson"
)

// newHTTPClient returns a new HTTP client that is configured according to the
// proxy and TLS settings in the associated connection configuration.
func newHTTPClient(cfg *pod.Config) (*http.Client, error) {
	// Configure proxy if needed.
	var dial func(network, addr string) (net.Conn, error)
	if *cfg.Proxy != "" {
		proxy := &socks.Proxy{
			Addr:     *cfg.Proxy,
			Username: *cfg.ProxyUser,
			Password: *cfg.ProxyPass,
		}
		dial = func(network, addr string) (net.Conn, error) {
			c, err := proxy.Dial(network, addr)
			if err != nil {
				log.ERROR(err)
				return nil, err
			}
			return c, nil
		}
	}
	// Configure TLS if needed.
	var tlsConfig *tls.Config
	if *cfg.TLS && *cfg.RPCCert != "" {
		pem, err := ioutil.ReadFile(*cfg.RPCCert)
		if err != nil {
			log.ERROR(err)
			return nil, err
		}
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(pem)
		tlsConfig = &tls.Config{
			RootCAs: pool,
			// nolint
			InsecureSkipVerify: *cfg.TLSSkipVerify,
		}
	}
	// Create and return the new HTTP client potentially configured with a
	// proxy and TLS.
	client := http.Client{
		Transport: &http.Transport{
			Dial:            dial,
			TLSClientConfig: tlsConfig,
		},
	}
	return &client, nil
}

// sendPostRequest sends the marshalled JSON-RPC command using HTTP-POST mode
// to the server described in the passed config struct.  It also attempts to
// unmarshal the response as a JSON-RPC response and returns either the result
// field or the error field depending on whether or not there is an error.
func sendPostRequest(marshalledJSON []byte, cx *conte.Xt) ([]byte, error) {
	// Generate a request to the configured RPC server.
	protocol := "http"
	if *cx.Config.TLS {
		protocol = "https"
	}
	serverAddr := *cx.Config.RPCConnect
	if *cx.Config.Wallet {
		serverAddr = *cx.Config.WalletServer
		log.Println("using wallet server", serverAddr)
	}
	url := protocol + "://" + serverAddr
	bodyReader := bytes.NewReader(marshalledJSON)
	httpRequest, err := http.NewRequest("POST", url, bodyReader)
	if err != nil {
		log.ERROR(err)
		return nil, err
	}
	httpRequest.Close = true
	httpRequest.Header.Set("Content-Type", "application/json")
	// Configure basic access authorization.
	httpRequest.SetBasicAuth(*cx.Config.Username, *cx.Config.Password)
	// Create the new HTTP client that is configured according to the user
	// - specified options and submit the request.
	httpClient, err := newHTTPClient(cx.Config)
	if err != nil {
		log.ERROR(err)
		return nil, err
	}
	httpResponse, err := httpClient.Do(httpRequest)
	if err != nil {
		log.ERROR(err)
		return nil, err
	}
	// Read the raw bytes and close the response.
	respBytes, err := ioutil.ReadAll(httpResponse.Body)
	httpResponse.Body.Close()
	if err != nil {
		log.ERROR(err)
		err = fmt.Errorf("error reading json reply: %v", err)
		log.ERROR(err)
		return nil, err
	}
	// Handle unsuccessful HTTP responses
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		// Generate a standard error to return if the server body is empty.
		// This should not happen very often,
		// but it's better than showing nothing in case the target server has
		// a poor implementation.
		if len(respBytes) == 0 {
			return nil, fmt.Errorf("%d %s", httpResponse.StatusCode,
				http.StatusText(httpResponse.StatusCode))
		}
		return nil, fmt.Errorf("%s", respBytes)
	}
	// Unmarshal the response.
	var resp btcjson.Response
	if err := js.Unmarshal(respBytes, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp.Result, nil
}
