package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode"

	"proxy-box/internal/core"
)

func publicErr(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "操作已取消"
	case errors.Is(err, core.ErrAlreadyRunning):
		return "内核已在运行"
	case errors.Is(err, core.ErrNotRunning):
		return "内核未运行"
	case errors.Is(err, core.ErrHashMismatch):
		return "内核校验失败"
	case errors.Is(err, core.ErrNoPE):
		return "文件不是有效的 Windows 可执行程序"
	case errors.Is(err, core.ErrGeoHTML):
		return "地理数据下载到了网页，不是规则库"
	case errors.Is(err, core.ErrGeoTooSmall):
		return "地理数据文件不完整"
	case errors.Is(err, core.ErrGeoPreflight):
		return "地理数据无法加载，已关闭绕过大陆"
	}
	var syntax *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &syntax) || errors.As(err, &typeErr) {
		return "请求格式不正确"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "JSON"):
		return "请求格式不正确"
	case strings.Contains(msg, "subscription url"):
		return "订阅地址无效，需要 http 或 https 链接"
	case strings.Contains(msg, "subscription body is empty"):
		return "订阅内容为空"
	case strings.Contains(msg, "subscription body is html"):
		return "订阅服务器返回了网页，不是节点列表"
	case strings.Contains(msg, "subscription has no nodes"):
		return "订阅里没有可用节点"
	case strings.Contains(msg, "subscription body exceeds"):
		return "订阅内容超过 16MB"
	case strings.Contains(msg, "subscription status"):
		return "订阅下载失败，服务器返回错误"
	case strings.Contains(msg, "providers dir is empty"):
		return "订阅保存目录无效"
	case strings.Contains(msg, "subscription id"):
		return "订阅标识无效"
	case strings.Contains(msg, "mihomo not ready"):
		return "内核没有在限定时间内就绪"
	case strings.Contains(msg, "ports must differ"):
		return "混合端口、控制端口和 AI 端口不能相同"
	case strings.Contains(msg, "no free port"), strings.Contains(msg, "out of range"):
		return "没有可用端口"
	case strings.Contains(msg, "mode is empty"), strings.Contains(msg, "mode "):
		return "模式只能是 rule、global 或 direct"
	case strings.Contains(msg, "want .exe or .zip"):
		return "请选择 mihomo.exe 或 zip 压缩包"
	case strings.Contains(msg, "geo download"):
		return "地理数据下载失败"
	case strings.Contains(msg, "download stalled"):
		return "内核下载超时"
	case strings.Contains(msg, "download failed"), strings.Contains(msg, "sha256"):
		return "内核下载失败"
	case strings.Contains(msg, "unreachable"):
		return "通过本地代理下载内核失败"
	case containsChinese(msg):
		return msg
	default:
		return "操作失败：" + msg
	}
}

func startFailure(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "已取消启动内核"
	}
	msg := publicErr(err)
	if strings.Contains(msg, "mihomo.exe/.zip") {
		return msg
	}
	return msg + "。请手动导入 mihomo.exe/.zip"
}

func containsChinese(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}
