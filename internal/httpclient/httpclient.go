package httpclient

import (
	"net"
	"net/http"
	"time"

	"github.com/HexSilence/dorker/internal/config"
)

func New(c config.HTTPConfig) *http.Client {
	return &http.Client{Timeout: c.RequestTimeout, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: c.ConnectTimeout, KeepAlive: 30 * time.Second}).DialContext, ForceAttemptHTTP2: true, MaxIdleConns: 100, MaxIdleConnsPerHost: 10, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: c.RequestTimeout}}
}
