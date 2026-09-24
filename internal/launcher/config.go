// Package browser manages Chrome processes and extension configuration.
package browser

// Config 描述 browser 包启动或附着浏览器实例时使用的配置。
//
// 同一个 ChromeUserDir 会被视为同一个浏览器实例。
type Config struct {
	// ChromeExecutable 是 Chrome 或 Chromium 可执行文件路径。
	// 为空时会在系统默认位置自动查找。
	ChromeExecutable string `yaml:"chrome_executable"`
	// ChromeUserDir 是浏览器用户数据目录，也是实例身份的判定依据。
	ChromeUserDir string `yaml:"chrome_user_dir"`

	// ChromeHooks 用于在启动新实例前生成扩展目录内容。
	ChromeHooks []ChromeExtHook `yaml:"-"`
	// CustomArgs 会追加到浏览器启动参数中。
	// 该字段不能覆盖 remote debugging 相关参数。
	CustomArgs []string `yaml:"custom_args"`

	// CAFingerPrints 会转换成忽略证书错误的 SPKI 指纹参数。
	CAFingerPrints []string `yaml:"ca_fingerprints"`
	// FakeHosts 会转换成 host resolver rules。
	FakeHosts map[string]string `yaml:"fake_hosts"`
}

// ChromeExtHook 在启动新实例前修改扩展 manifest 并写入扩展目录。
type ChromeExtHook func(map[string]any, string) error
