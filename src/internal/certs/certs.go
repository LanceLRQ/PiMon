// Package certs 负责自签名证书的生成与读取，以及证书公钥（SPKI）指纹的
// 计算、格式化、解析与钉扎校验。hub 与 agent 共用。
package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const defaultCommonName = "PiMon"

// Env 是证书 SAN 的环境来源，字段为空时使用系统实现，测试可注入。
type Env struct {
	Hostname       func() (string, error)
	InterfaceAddrs func() ([]net.Addr, error)
}

// Options 是证书生成与加载的选项。
type Options struct {
	// CommonName 是新证书的主题 CN，为空时使用 "PiMon"。
	CommonName string
	// Env 是 SAN 的环境来源，字段为空时使用系统实现。
	Env Env
}

// LoadOrCreate 在证书与私钥都存在时直接读取，否则生成新证书并落盘。
// 新证书有效期从 now 起 10 年。
//
// 钉扎基于证书公钥：已存在的文件原样加载，公钥不变；但删掉其中任一文件
// 会生成新密钥对，等同于换钥，所有已钉扎的对端都需要重新钉扎。
func LoadOrCreate(certPath, keyPath string, now time.Time, opts Options) (tls.Certificate, error) {
	if opts.Env.Hostname == nil {
		opts.Env.Hostname = os.Hostname
	}
	if opts.Env.InterfaceAddrs == nil {
		opts.Env.InterfaceAddrs = net.InterfaceAddrs
	}
	if opts.CommonName == "" {
		opts.CommonName = defaultCommonName
	}
	if fileExists(certPath) && fileExists(keyPath) {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("读取证书失败: %w", err)
		}
		return cert, nil
	}
	return create(certPath, keyPath, now, opts)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func create(certPath, keyPath string, now time.Time, opts Options) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return tls.Certificate{}, err
	}
	dnsNames, ips := subjectAltNames(opts.Env)
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: opts.CommonName},
		NotBefore:             now,
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
		IPAddresses:           ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	// 先写私钥再写证书：证书存在即意味着私钥已就绪。
	if err := writeAtomic(keyPath, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, err
	}
	if err := writeAtomic(certPath, certPEM, 0o644); err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(certPEM, keyPEM)
}

// subjectAltNames 汇总主机名、<主机名>.local、localhost、回环与各网卡 IP。
func subjectAltNames(env Env) ([]string, []net.IP) {
	dns := []string{"localhost"}
	ips := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	if h, err := env.Hostname(); err == nil && h != "" {
		dns = append(dns, h, h+".local")
	}
	if addrs, err := env.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && !containsIP(ips, ip) {
				ips = append(ips, ip)
			}
		}
	}
	return dns, ips
}

func containsIP(list []net.IP, ip net.IP) bool {
	for _, x := range list {
		if x.Equal(ip) {
			return true
		}
	}
	return false
}

// writeAtomic 写临时文件后 rename，避免留下半截文件。
func writeAtomic(path string, data []byte, perm os.FileMode) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Chmod(tmp, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
