package utils

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// DecodeJSONStrict 将单个 JSON 值解码到 dst，拒绝结构体中的未知字段及尾部多余内容。
// JSON 类型由 dst 决定；必填字段等业务校验由调用方负责。
func DecodeJSONStrict(input string, dst any) error {
	decoder := json.NewDecoder(strings.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("输入必须是单个 JSON 值")
	}
	return nil
}
