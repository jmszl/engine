package engine // import "m7s.live/engine/v4"

import (
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	. "github.com/logrusorgru/aurora"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/yaml.v3"
	"m7s.live/engine/v4/config"
	"m7s.live/engine/v4/lang"
	"m7s.live/engine/v4/log"
	"m7s.live/engine/v4/util"
)

var (
	SysInfo struct {
		StartTime time.Time //启动时间
		LocalIP   string
		Version   string
	}
	ExecPath = os.Args[0]
	ExecDir  = filepath.Dir(ExecPath)
	// ConfigRaw 配置信息的原始数据
	ConfigRaw    []byte
	Plugins      = make(map[string]*Plugin) // Plugins 所有的插件配置
	plugins      []*Plugin                  //插件列表
	EngineConfig = &GlobalConfig{}
	Engine       = InstallPlugin(EngineConfig)
	SettingDir   = filepath.Join(ExecDir, ".m7s")           //配置缓存目录，该目录按照插件名称作为文件名存储修改过的配置
	MergeConfigs = []string{"Publish", "Subscribe", "HTTP"} //需要合并配置的属性项，插件若没有配置则使用全局配置
	EventBus     chan any
	apiList      []string //注册到引擎的API接口列表
)

func init() {
	if settingDir := os.Getenv("M7S_SETTING_DIR"); settingDir != "" {
		SettingDir = settingDir
	}
	if conn, err := net.Dial("udp", "114.114.114.114:80"); err == nil {
		SysInfo.LocalIP, _, _ = strings.Cut(conn.LocalAddr().String(), ":")
	}
}

// Run 启动Monibuca引擎，传入总的Context，可用于关闭所有
func Run(ctx context.Context, configFile string) (err error) {
	SysInfo.StartTime = time.Now()
	SysInfo.Version = Engine.Version
	Engine.Context = ctx
	if _, err = os.Stat(configFile); err != nil {
		configFile = filepath.Join(ExecDir, configFile)
	}
	if err = util.CreateShutdownScript(); err != nil {
		log.Error("create shutdown script error:", err)
	}
	if ConfigRaw, err = ioutil.ReadFile(configFile); err != nil {
		log.Warn("read config file error:", err.Error())
	}
	if err = os.MkdirAll(SettingDir, 0766); err != nil {
		log.Error("create dir .m7s error:", err)
		return
	}
	log.Info("Ⓜ starting engine:", Blink(Engine.Version))
	var cg config.Config
	if ConfigRaw != nil {
		if err = yaml.Unmarshal(ConfigRaw, &cg); err == nil {
			Engine.RawConfig = cg.GetChild("global")
			if b, err := yaml.Marshal(Engine.RawConfig); err == nil {
				Engine.Yaml = string(b)
			}
			//将配置信息同步到结构体
			Engine.RawConfig.Unmarshal(&EngineConfig.Engine)
		} else {
			log.Error("parsing yml error:", err)
		}
	}
	var logger log.Logger
	log.LocaleLogger = logger.Lang(lang.Get(EngineConfig.LogLang))
	if EngineConfig.LogLevel == "trace" {
		log.Trace = true
		log.LogLevel.SetLevel(zap.DebugLevel)
	} else {
		loglevel, err := zapcore.ParseLevel(EngineConfig.LogLevel)
		if err != nil {
			logger.Error("parse log level error:", zap.Error(err))
			loglevel = zapcore.InfoLevel
		}
		log.LogLevel.SetLevel(loglevel)
	}

	Engine.Logger = log.LocaleLogger.Named("engine")
	// 使得RawConfig具备全量配置信息，用于合并到插件配置中
	Engine.RawConfig = config.Struct2Config(&EngineConfig.Engine, "GLOBAL")
	Engine.assign()
	Engine.Logger.Debug("", zap.Any("config", EngineConfig))
	EventBus = make(chan any, EngineConfig.EventBusSize)
	go EngineConfig.Listen(Engine)
	for _, plugin := range plugins {
		plugin.Logger = log.LocaleLogger.Named(plugin.Name)
		if os.Getenv(strings.ToUpper(plugin.Name)+"_ENABLE") == "false" {
			plugin.Disabled = true
			plugin.Warn("disabled by env")
			continue
		}
		plugin.Info("initialize", zap.String("version", plugin.Version))
		userConfig := cg.GetChild(plugin.Name)
		if userConfig != nil {
			if b, err := yaml.Marshal(userConfig); err == nil {
				plugin.Yaml = string(b)
			}
		}
		if defaultYaml := reflect.ValueOf(plugin.Config).Elem().FieldByName("DefaultYaml"); defaultYaml.IsValid() {
			if err := yaml.Unmarshal([]byte(defaultYaml.String()), &plugin.RawConfig); err != nil {
				log.Error("parsing default config error:", err)
			}
		}
		if plugin.Yaml != "" {
			yaml.Unmarshal([]byte(plugin.Yaml), &plugin.RawConfig)
		}
		plugin.assign()
	}
	version := Engine.Version
	if ver, ok := ctx.Value("version").(string); ok && ver != "" && ver != "dev" {
		version = ver
	}
	if EngineConfig.LogLang == "zh" {
		log.Info("monibuca ", version, Green(" 启动成功"))
	} else {
		log.Info("monibuca ", version, Green(" start success"))
	}
	var enabledPlugins, disabledPlugins []*Plugin
	for _, plugin := range plugins {
		if plugin.Disabled {
			disabledPlugins = append(disabledPlugins, plugin)
		} else {
			enabledPlugins = append(enabledPlugins, plugin)
		}
	}
	if EngineConfig.LogLang == "zh" {
		fmt.Print("已运行的插件：")
	} else {
		fmt.Print("enabled plugins：")
	}
	for _, plugin := range enabledPlugins {
		fmt.Print(Colorize(" "+plugin.Name+" ", BlackFg|GreenBg|BoldFm), " ")
	}
	fmt.Println()
	if EngineConfig.LogLang == "zh" {
		fmt.Print("已禁用的插件：")
	} else {
		fmt.Print("disabled plugins：")
	}
	for _, plugin := range disabledPlugins {
		fmt.Print(Colorize(" "+plugin.Name+" ", BlackFg|RedBg|CrossedOutFm), " ")
	}

	for _, plugin := range enabledPlugins {
		plugin.Config.OnEvent(EngineConfig) //引擎初始化完成后，通知插件
	}
	for {
		select {
		case event := <-EventBus:
			ts := time.Now()
			for _, plugin := range enabledPlugins {
				ts := time.Now()
				plugin.Config.OnEvent(event)
				if cost := time.Since(ts); cost > time.Millisecond*100 {
					plugin.Warn("event cost too much time", zap.String("event", fmt.Sprintf("%v", event)), zap.Duration("cost", cost))
				}
			}
			EngineConfig.OnEvent(event)
			if cost := time.Since(ts); cost > time.Millisecond*100 {
				log.Warn("event cost too much time", zap.String("event", fmt.Sprintf("%v", event)), zap.Duration("cost", cost))
			}
		case <-ctx.Done():
			return
		}
	}
}
