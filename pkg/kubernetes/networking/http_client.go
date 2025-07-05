package networking

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"
)

var privateIPBlocks []*net.IPNet

// HttpTimeout is the timeout for outgoing HTTP requests
const HttpTimeout = 30 * time.Second

// Dialer control function for validating addresses prior to connection
func protectedDialerControl(_, address string, _ syscall.RawConn) error {

	err := AddressReferencesPrivateIp(address)
	if err != nil {
		return err
	}

	return nil
}

// ValidatingTransport is for validating URLs prior to request
type ValidatingTransport struct {
	Transport http.RoundTripper
}

// RoundTrip validates the request URL prior to forwarding
func (t *ValidatingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Check for valid URL specification
	parsedUrl, err := url.Parse(req.URL.String())
	if err != nil {
		fmt.Print(err)
		return nil, fmt.Errorf("the supplied URL %s is malformed", req.URL.String())
	}

	// Check for HTTPS scheme
	if parsedUrl.Scheme != "https" {
		return nil, fmt.Errorf("the supplied URL %s is not HTTPS scheme", req.URL.String())
	}

	return t.Transport.RoundTrip(req)
}

// GetHttpClient returns a new http client which uses a protected dialer and URL validation by default
func GetHttpClient(allowPrivateIp bool) *http.Client {

	var transport *http.Transport
	if !allowPrivateIp {
		transport = &http.Transport{
			DialContext: (&net.Dialer{
				Control: protectedDialerControl,
			}).DialContext,
		}
	} else {
		transport = &http.Transport{}
	}

	client := &http.Client{
		Transport: &ValidatingTransport{
			Transport: transport,
		},
		Timeout: HttpTimeout,
	}

	return client
}
