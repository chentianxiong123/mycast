// Package tv 中间件当"发送端"：用 goupnp 控制电视（DLNA AVTransport）。
package tv

import (
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/huin/goupnp/dcps/av1"
	"github.com/huin/goupnp/soap"

	"mycast/internal/ssdp"
	"mycast/internal/upnp"
)

// Controller 电视控制句柄
type Controller struct {
	mu  sync.Mutex
	dev *ssdp.Device
	av  *av1.AVTransport1
	rc  *av1.RenderingControl1
	// Status 缓存（500ms 内复用，避免每秒 3 次 SOAP 慢）
	stMu   sync.Mutex
	stCache map[string]string
	stAt   time.Time
}

// NewController 根据发现的设备创建控制句柄
func NewController(dev *ssdp.Device) (*Controller, error) {
	u, err := url.Parse(dev.Location)
	if err != nil {
		return nil, err
	}
	avs, err := av1.NewAVTransport1ClientsByURL(u)
	if err != nil || len(avs) == 0 {
		return nil, fmt.Errorf("no AVTransport service on %s: %w", dev.Name, err)
	}
	c := &Controller{dev: dev, av: avs[0]}
	if rcs, err := av1.NewRenderingControl1ClientsByURL(u); err == nil && len(rcs) > 0 {
		c.rc = rcs[0]
	}
	return c, nil
}

// Push 设置播放地址并开始播放。
// 国产电视（如 HappyCast）description.xml 里的 controlURL 是相对路径
// `_urn:...AVTransport_control`，goupnp 拼出来虽然奇怪但**是对的**——实测能正常
// 返回 GetTransportInfo。所以原始地址优先，连接类错误重试 3 次；
// 只有 SOAP fault（有效响应）才说明地址错了，此时才换候选。
func (c *Controller) Push(uri, title, contentType string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	didl := upnp.DIDLLite(uri, title, contentType)
	var lastErr error
	for i := 0; i < 3; i++ {
		err := c.av.SetAVTransportURI(0, uri, didl)
		if err == nil {
			if err = c.av.Play(0, "1"); err == nil {
				return nil
			}
		}
		lastErr = err
		var sf *soap.SOAPFaultError
		if errors.As(err, &sf) {
			break // 有效 SOAP 响应：地址对，是 action/参数问题，别换地址
		}
		time.Sleep(250 * time.Millisecond) // 电视偶发 connection reset，重试
	}
	// 连接类错误持续失败才尝试常见候选路径
	if c.dev == nil || c.dev.Location == "" {
		return lastErr
	}
	u, err := url.Parse(c.dev.Location)
	if err != nil {
		return lastErr
	}
	base := url.URL{Scheme: u.Scheme, Host: u.Host}
	for _, p := range []string{
		"/AVTransport/control", "/upnp/control/AVTransport",
		"/MediaRenderer/AVTransport/Control", "/ctl/AVTransport",
	} {
		ep := base
		ep.Path = p
		c.av.ServiceClient.SOAPClient.EndpointURL = ep
		if e := c.av.SetAVTransportURI(0, uri, didl); e != nil {
			lastErr = e
			continue
		}
		if e := c.av.Play(0, "1"); e != nil {
			lastErr = e
			continue
		}
		return nil
	}
	return fmt.Errorf("AVTransport 推送失败: %w", lastErr)
}

// Control 播放控制
func (c *Controller) Control(action, target string, volume int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch action {
	case "play":
		return c.av.Play(0, "1")
	case "pause":
		return c.av.Pause(0)
	case "stop":
		return c.av.Stop(0)
	case "seek":
		return c.av.Seek(0, "REL_TIME", target)
	case "volume":
		if c.rc == nil {
			return fmt.Errorf("device has no RenderingControl")
		}
		return c.rc.SetVolume(0, "Master", uint16(volume))
	}
	return fmt.Errorf("unknown action: %s", action)
}

// Status 查播放状态
func (c *Controller) Status() map[string]string {
	// 缓存 500ms（避免每秒 3 次 SOAP 慢）
	c.stMu.Lock()
	if c.stCache != nil && time.Since(c.stAt) < 500*time.Millisecond {
		out := c.stCache
		c.stMu.Unlock()
		return out
	}
	c.stMu.Unlock()

	out := map[string]string{"device": c.dev.Name}
	state, status, _, err := c.av.GetTransportInfo(0)
	if err == nil {
		out["state"] = state
		out["status"] = status
	}
	if _, dur, _, uri, rel, _, _, _, err := c.av.GetPositionInfo(0); err == nil {
		out["duration"] = dur
		out["uri"] = uri
		out["position"] = rel
	}
	if c.rc != nil {
		if v, err := c.rc.GetVolume(0, "Master"); err == nil {
			out["volume"] = fmt.Sprint(v)
		}
	}

	c.stMu.Lock()
	c.stCache = out
	c.stAt = time.Now()
	c.stMu.Unlock()
	return out
}
