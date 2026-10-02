package proxy

import "crypto/x509"

// WithRootCAs 供外部测试包为 https 代理注入受信根证书池。
func WithRootCAs(p *Proxy, pool *x509.CertPool) *Proxy {
	p.rootCAs = pool
	return p
}
