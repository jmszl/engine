package engine

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"m7s.live/engine/v4/codec"
	"m7s.live/engine/v4/config"
	"m7s.live/engine/v4/util"
)

const (
	NO_SUCH_CONIFG = "no such config"
	NO_SUCH_STREAM = "no such stream"
)

type GlobalConfig struct {
	config.Engine
}

func ShouldYaml(r *http.Request) bool {
	format := r.URL.Query().Get("format")
	return r.URL.Query().Get("yaml") != "" || format == "yaml"
}

func (conf *GlobalConfig) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/favicon.ico" {
		http.ServeFile(rw, r, "favicon.ico")
		return
	}
	rw.Write([]byte("Monibuca API Server\n"))
	for _, api := range apiList {
		rw.Write([]byte(api + "\n"))
	}
}

func fetchSummary() *Summary {
	return &Sum
}

func (conf *GlobalConfig) API_summary(rw http.ResponseWriter, r *http.Request) {
	y := ShouldYaml(r)
	if r.Header.Get("Accept") == "text/event-stream" {
		Sum.Add()
		defer Sum.Done()
		if y {
			util.ReturnYaml(fetchSummary, time.Second, rw, r)
		} else {
			util.ReturnJson(fetchSummary, time.Second, rw, r)
		}
	} else {
		if !Sum.Running() {
			Sum.collect()
		}
		Sum.rw.RLock()
		defer Sum.rw.RUnlock()
		if y {
			if err := yaml.NewEncoder(rw).Encode(&Sum); err != nil {
				http.Error(rw, err.Error(), http.StatusInternalServerError)
			}
		} else if err := json.NewEncoder(rw).Encode(&Sum); err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
		}
	}
}

func (conf *GlobalConfig) API_plugins(rw http.ResponseWriter, r *http.Request) {
	if ShouldYaml(r) {
		if err := yaml.NewEncoder(rw).Encode(Plugins); err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
		}
	} else if err := json.NewEncoder(rw).Encode(Plugins); err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
	}
}

func (conf *GlobalConfig) API_stream(rw http.ResponseWriter, r *http.Request) {
	if streamPath := r.URL.Query().Get("streamPath"); streamPath != "" {
		if s := Streams.Get(streamPath); s != nil {
			if ShouldYaml(r) {
				util.ReturnYaml(func() *Stream { return s }, time.Second, rw, r)
			} else {
				util.ReturnJson(func() *Stream { return s }, time.Second, rw, r)
			}
		} else {
			http.Error(rw, NO_SUCH_STREAM, http.StatusNotFound)
		}
	} else {
		http.Error(rw, "no streamPath", http.StatusBadRequest)
	}
}

func (conf *GlobalConfig) API_sysInfo(rw http.ResponseWriter, r *http.Request) {
	if err := json.NewEncoder(rw).Encode(&SysInfo); err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
	}
}

func (conf *GlobalConfig) API_closeStream(w http.ResponseWriter, r *http.Request) {
	if streamPath := r.URL.Query().Get("streamPath"); streamPath != "" {
		if s := Streams.Get(streamPath); s != nil {
			s.Close()
			w.Write([]byte("ok"))
		} else {
			http.Error(w, NO_SUCH_STREAM, http.StatusNotFound)
		}
	} else {
		http.Error(w, "no streamPath", http.StatusBadRequest)
	}
}

// API_getConfig 获取指定的配置信息
func (conf *GlobalConfig) API_getConfig(w http.ResponseWriter, r *http.Request) {
	var p *Plugin
	var q = r.URL.Query()
	if configName := q.Get("name"); configName != "" {
		if c, ok := Plugins[configName]; ok {
			p = c
		} else {
			http.Error(w, NO_SUCH_CONIFG, http.StatusNotFound)
			return
		}
	} else {
		p = Engine
	}
	if ShouldYaml(r) {
		mm, err := yaml.Marshal(p.RawConfig)
		if err != nil {
			mm = []byte("")
		}
		json.NewEncoder(w).Encode(struct {
			File     string
			Modified string
			Merged   string
		}{
			p.Yaml, p.modifiedYaml, string(mm),
		})
	} else if err := json.NewEncoder(w).Encode(p.RawConfig); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// API_modifyConfig 修改并保存配置
func (conf *GlobalConfig) API_modifyConfig(w http.ResponseWriter, r *http.Request) {
	var p *Plugin
	var q = r.URL.Query()
	var err error
	if configName := q.Get("name"); configName != "" {
		if c, ok := Plugins[configName]; ok {
			p = c
		} else {
			http.Error(w, NO_SUCH_CONIFG, http.StatusNotFound)
			return
		}
	} else {
		p = Engine
	}
	if ShouldYaml(r) {
		err = yaml.NewDecoder(r.Body).Decode(&p.Modified)
	} else {
		err = json.NewDecoder(r.Body).Decode(&p.Modified)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	} else if err = p.Save(); err == nil {
		p.RawConfig.Assign(p.Modified)
		out, err := yaml.Marshal(p.Modified)
		if err == nil {
			p.modifiedYaml = string(out)
		}
		w.Write([]byte("ok"))
	} else {
		w.Write([]byte(err.Error()))
	}
}

// API_updateConfig 热更新配置
func (conf *GlobalConfig) API_updateConfig(w http.ResponseWriter, r *http.Request) {
	var p *Plugin
	var q = r.URL.Query()
	if configName := q.Get("name"); configName != "" {
		if c, ok := Plugins[configName]; ok {
			p = c
		} else {
			http.Error(w, NO_SUCH_CONIFG, http.StatusNotFound)
			return
		}
	} else {
		p = Engine
	}
	p.Update(p.Modified)
	w.Write([]byte("ok"))
}

func (conf *GlobalConfig) API_list_pull(w http.ResponseWriter, r *http.Request) {
	util.ReturnJson(func() (result []any) {
		Pullers.Range(func(key, value any) bool {
			result = append(result, key)
			return true
		})
		return
	}, time.Second, w, r)
}

func (conf *GlobalConfig) API_list_push(w http.ResponseWriter, r *http.Request) {
	util.ReturnJson(func() (result []any) {
		Pushers.Range(func(key, value any) bool {
			result = append(result, value)
			return true
		})
		return
	}, time.Second, w, r)
}

func (conf *GlobalConfig) API_stopPush(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pusher, ok := Pushers.Load(q.Get("url"))
	if ok {
		pusher.(IPusher).Stop()
		w.Write([]byte("ok"))
	} else {
		http.Error(w, "no such pusher", http.StatusNotFound)
	}
}

func (conf *GlobalConfig) API_replay_rtpdump(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	streamPath := q.Get("streamPath")
	if streamPath == "" {
		streamPath = "dump/rtsp"
	}
	dumpFile := q.Get("dump")
	if dumpFile == "" {
		dumpFile = streamPath + ".rtpdump"
	}
	cv := q.Get("vcodec")
	ca := q.Get("acodec")
	cvp := q.Get("vpayload")
	cap := q.Get("apayload")
	var pub RTPDumpPublisher
	i, _ := strconv.ParseInt(cvp, 10, 64)
	pub.VPayloadType = byte(i)
	i, _ = strconv.ParseInt(cap, 10, 64)
	pub.APayloadType = byte(i)
	switch cv {
	case "h264":
		pub.VCodec = codec.CodecID_H264
	case "h265":
		pub.VCodec = codec.CodecID_H265
	}
	switch ca {
	case "aac":
		pub.ACodec = codec.CodecID_AAC
	case "pcma":
		pub.ACodec = codec.CodecID_PCMA
	case "pcmu":
		pub.ACodec = codec.CodecID_PCMU
	}
	ss := strings.Split(dumpFile, ",")
	if len(ss) > 1 {
		if err := Engine.Publish(streamPath, &pub); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		} else {
			for _, s := range ss {
				f, err := os.Open(s)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				go pub.Feed(f)
			}
			w.Write([]byte("ok"))
		}
	} else {
		f, err := os.Open(dumpFile)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := Engine.Publish(streamPath, &pub); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		} else {
			pub.SetIO(f)
			w.Write([]byte("ok"))
			go pub.Feed(f)
		}
	}
}

func (conf *GlobalConfig) API_replay_ts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	streamPath := q.Get("streamPath")
	if streamPath == "" {
		streamPath = "dump/ts"
	}
	dumpFile := q.Get("dump")
	if dumpFile == "" {
		dumpFile = streamPath + ".ts"
	}
	f, err := os.Open(dumpFile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var pub TSPublisher
	if err := Engine.Publish(streamPath, &pub); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		pub.SetIO(f)
		go pub.Feed(f)
		w.Write([]byte("ok"))
	}
}

func (conf *GlobalConfig) API_replay_mp4(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	streamPath := q.Get("streamPath")
	if streamPath == "" {
		streamPath = "dump/mp4"
	}
	dumpFile := q.Get("dump")
	if dumpFile == "" {
		dumpFile = streamPath + ".mp4"
	}
	var pub MP4Publisher
	f, err := os.Open(dumpFile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := Engine.Publish(streamPath, &pub); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		pub.SetIO(f)
		w.Write([]byte("ok"))
		go pub.ReadMP4Data(f)
	}
}

func (conf *GlobalConfig) API_insertSEI(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	streamPath := q.Get("streamPath")
	s := Streams.Get(streamPath)
	if s == nil {
		http.Error(w, NO_SUCH_STREAM, http.StatusNotFound)
		return
	}
	t := q.Get("type")
	tb, err := strconv.ParseInt(t, 10, 8)
	if err != nil {
		if t == "" {
			tb = 5
		} else {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	sei, err := io.ReadAll(r.Body)
	if err == nil {
		if s.Tracks.AddSEI(byte(tb), sei) {
			w.Write([]byte("ok"))
		} else {
			http.Error(w, "no sei track", http.StatusBadRequest)
		}
	} else {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}
