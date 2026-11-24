package httptool

import (
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"testing"

	utls "github.com/refraction-networking/utls"
)

// TestJA3Fingerprint 离线计算 ClientHello 的 JA3，验证 TLS 指纹伪装成 Chrome。
// utls.MarshalClientHello 不碰网络，测试离线稳定。若 utls 升级导致指纹变化，
// 这里会报警（防止回归到 Go 默认指纹——那会被 WAF 识别为爬虫）。
func TestJA3Fingerprint(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	uconn := utls.UClient(c1, &utls.Config{ServerName: "www.baidu.com"}, utls.HelloChrome_Auto)
	if err := uconn.BuildHandshakeState(); err != nil {
		t.Fatal(err)
	}
	if err := uconn.MarshalClientHello(); err != nil {
		t.Fatal(err)
	}
	raw := uconn.HandshakeState.Hello.Raw
	if len(raw) == 0 {
		t.Fatal("ClientHello 为空")
	}
	ja3 := computeJA3(raw)
	t.Logf("JA3 = %s (Chrome 系指纹)", ja3)
	// 关键断言：TLS1.3(771) + Chrome 常见套件 + 椭圆曲线 29-23-24（X25519/P256/P384）
	if !strings.HasPrefix(ja3, "771,") {
		t.Errorf("TLS 版本应为 771(TLS1.3)，实际: %s", ja3)
	}
	for _, need := range []string{"4865", "4866", "4867", "52393", "29-23-24"} {
		if !strings.Contains(ja3, need) {
			t.Errorf("JA3 缺少 Chrome 特征 %s，实际: %s", need, ja3)
		}
	}
	if strings.Contains(ja3, ",,,") {
		t.Errorf("JA3 字段缺失，实际: %s", ja3)
	}
}

// computeJA3 标准 JA3 算法：version,cipher_suites,extensions,elliptic_curves,ec_point_formats
func computeJA3(raw []byte) string {
	// Raw 从 handshake message 开始：raw[0]=0x01(ClientHello) + raw[1:4]=len(3B) + body
	// body = legacy_version(2) + random(32) + session_id_len(1) + ...
	if len(raw) < 4+2+32+1 {
		return "too short"
	}
	pos := 4 // 跳过 handshake msg type(1) + len(3)
	version := binary.BigEndian.Uint16(raw[pos : pos+2])
	pos += 2 + 32 // version + random

	// session id
	sidLen := int(raw[pos])
	pos += 1 + sidLen

	// cipher suites
	if pos+2 > len(raw) {
		return "truncated"
	}
	csLen := int(binary.BigEndian.Uint16(raw[pos : pos+2]))
	pos += 2
	csEnd := pos + csLen
	var ciphers []string
	for ; pos < csEnd; pos += 2 {
		cs := binary.BigEndian.Uint16(raw[pos : pos+2])
		if isGreased(cs) {
			continue
		}
		ciphers = append(ciphers, fmt.Sprint(cs))
	}

	// compression methods
	compLen := int(raw[pos])
	pos += 1 + compLen

	// extensions
	if pos+2 > len(raw) {
		return fmt.Sprintf("%d,%s,,,", version, strings.Join(ciphers, "-"))
	}
	extLen := int(binary.BigEndian.Uint16(raw[pos : pos+2]))
	pos += 2
	extEnd := pos + extLen
	var extTypes []string
	curves := ""
	pointFmt := ""
	for pos < extEnd {
		if pos+4 > extEnd {
			break
		}
		et := binary.BigEndian.Uint16(raw[pos : pos+2])
		el := int(binary.BigEndian.Uint16(raw[pos+2 : pos+4]))
		pos += 4
		if isGreased(et) {
			pos += el
			continue
		}
		extTypes = append(extTypes, fmt.Sprint(et))
		if et == 10 && pos+2 <= len(raw) { // supported_groups
			gl := int(binary.BigEndian.Uint16(raw[pos : pos+2]))
			var gs []string
			for i := 0; i < gl/2; i++ {
				g := binary.BigEndian.Uint16(raw[pos+2+i*2 : pos+4+i*2])
				if isGreased(g) {
					continue
				}
				gs = append(gs, fmt.Sprint(g))
			}
			curves = strings.Join(gs, "-")
		}
		if et == 11 && pos+1 <= len(raw) { // ec_point_formats
			pl := int(raw[pos])
			var fs []string
			for i := 0; i < pl; i++ {
				fs = append(fs, fmt.Sprint(raw[pos+1+i]))
			}
			pointFmt = strings.Join(fs, "-")
		}
		pos += el
	}
	return fmt.Sprintf("%d,%s,%s,%s,%s", version, strings.Join(ciphers, "-"),
		strings.Join(extTypes, "-"), curves, pointFmt)
}

func isGreased(v uint16) bool {
	// greased: 高字节=低字节=0x0a
	return v&0x0f0f == 0x0a0a
}
