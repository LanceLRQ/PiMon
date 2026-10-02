package schema

import (
	"fmt"
	"regexp"
)

// Compile 预编译 fields（含 object_list 子字段）里每个 Pattern，结果写回字段。
// 用于不经 YAML 解码得到的字段定义（如 agent 上报的 JSON manifest）：预编译后多协程并发校验
// 不会再惰性写入字段。遇到非法正则返回指出路径的错误，此时 fields 可能已部分编译。
func Compile(fields []Field) error {
	return compileLevel(fields, "config_schema")
}

func compileLevel(fields []Field, base string) error {
	for i := range fields {
		f := &fields[i]
		path := fmt.Sprintf("%s[%d]", base, i)
		if f.Pattern != "" {
			re, err := regexp.Compile(f.Pattern)
			if err != nil {
				return fmt.Errorf("%s.pattern 不是合法的正则: %w", path, err)
			}
			f.re = re
		}
		if err := compileLevel(f.Fields, path+".fields"); err != nil {
			return err
		}
	}
	return nil
}
