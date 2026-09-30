// Package runtime 定义插件各形态共用的运行时接口，以及内置插件的编译期注册表。
//
// 本包不依赖 hub 内部包，hub 与 agent 共用。
//
// 内置插件的写法：每个插件包内嵌自己的 plugin.yaml，在 init 里解析并注册：
//
//	//go:embed plugin.yaml
//	var manifestYAML []byte
//
//	func init() {
//		m, err := manifest.Parse(manifestYAML)
//		if err != nil {
//			panic(err) // 内嵌 manifest 写错属于编译期缺陷，启动即暴露
//		}
//		runtime.Register(&source{manifest: m})
//	}
//
// Source.Manifest 返回指向注册时 manifest 的指针，调用方只读使用。
package runtime
