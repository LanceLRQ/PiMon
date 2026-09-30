package plugindev

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// readConfig 按扩展名读取 JSON 或 YAML 配置。YAML 先转成与 JSON 一致的形状（数字为 float64），
// 这样与 hub 里实例配置的取值形状相同。path 为空返回空配置。
func readConfig(path string) (map[string]any, error) {
	if path == "" {
		return map[string]any{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件: %w", err)
	}
	var v any
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, fmt.Errorf("配置文件 %s 不是合法的 JSON: %w", path, err)
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &v); err != nil {
			return nil, fmt.Errorf("配置文件 %s 不是合法的 YAML: %w", path, err)
		}
		b, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("配置文件 %s 含有无法转为 JSON 的值: %w", path, err)
		}
		v = nil
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("配置文件 %s 的扩展名不受支持，请用 .json、.yaml 或 .yml", path)
	}
	if v == nil {
		return map[string]any{}, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("配置文件 %s 的顶层必须是对象", path)
	}
	return m, nil
}
