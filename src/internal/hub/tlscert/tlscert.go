// Package tlscert 是中枢证书的薄封装：证书同时用于网页 HTTPS 与 agent 通道，
// 生成与指纹逻辑在 internal/certs。
package tlscert

import (
	"crypto/tls"
	"time"

	"github.com/LanceLRQ/PiMon/src/internal/certs"
)

// Env 是证书 SAN 的环境来源。
type Env = certs.Env

const commonName = "PiMon Hub"

// LoadOrCreate 在证书与私钥都存在时直接读取，否则生成新证书并落盘。
func LoadOrCreate(certPath, keyPath string, now time.Time, env Env) (tls.Certificate, error) {
	return certs.LoadOrCreate(certPath, keyPath, now, certs.Options{CommonName: commonName, Env: env})
}
