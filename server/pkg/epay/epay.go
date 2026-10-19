package epay

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Sign 生成易支付（彩虹易支付等）V1 签名
// 规则：过滤 sign / sign_type / 空值，按参数名 ASCII 升序拼接 k=v&...，末尾直接拼接商户密钥，md5 小写
func Sign(params map[string]string, key string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || k == "sign_type" || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(params[k])
	}
	sb.WriteString(key)

	sum := md5.Sum([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// Verify 校验回调签名
func Verify(params map[string]string, key string) bool {
	sign := strings.ToLower(params["sign"])
	if sign == "" {
		return false
	}
	return sign == Sign(params, key)
}

// SubmitURL 规范化支付网关提交地址
func SubmitURL(apiURL string) string {
	apiURL = strings.TrimSpace(apiURL)
	if apiURL == "" {
		return ""
	}
	if strings.Contains(apiURL, ".php") {
		return apiURL
	}
	return strings.TrimRight(apiURL, "/") + "/submit.php"
}

// BuildPayURL 构建带签名的支付跳转地址
func BuildPayURL(apiURL string, params map[string]string, key string) (string, error) {
	base := SubmitURL(apiURL)
	if base == "" {
		return "", fmt.Errorf("支付网关地址为空")
	}
	params["sign"] = Sign(params, key)
	params["sign_type"] = "MD5"

	vals := url.Values{}
	for k, v := range params {
		vals.Set(k, v)
	}
	return base + "?" + vals.Encode(), nil
}
