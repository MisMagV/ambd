package proxy

import (
	disc "github.com/jeffjen/go-discovery"
	"github.com/jeffjen/go-libkv/libkv"
	"github.com/jeffjen/go-proxy/proxy"

	log "github.com/Sirupsen/logrus"
	etcd "github.com/coreos/etcd/client"
	ctx "golang.org/x/net/context"

	"encoding/json"
)

var (
	ProxyConfigKey string

	ConfigReset ctx.CancelFunc
)

func get(value string) (targets []*Info) {
	targets = make([]*Info, 0)
	if err := json.Unmarshal([]byte(value), &targets); err != nil {
		log.WithFields(log.Fields{"err": err}).Warning("bad proxy spec")
		targets = nil
	}
	return
}

func doReloadbyConfig(targets []*Info) {
	it, mod := Store.IterateW()
	for elem := range it {
		mod <- &libkv.Value{R: true}
		elem.X.(*Info).Cancel()
	}
	for _, meta := range targets {
		Listen(meta)
	}
}

func doWatch(c ctx.Context, watcher etcd.Watcher) <-chan []*Info {
	v := make(chan []*Info)
	go func() {
		evt, err := watcher.Next(c)
		if err != nil {
			log.Debug(err)
			close(v)
		} else if evt.Node.Dir {
			log.WithFields(log.Fields{"key": evt.Node.Key}).Warning("not a valid node")
			v <- make([]*Info, 0)
		} else {
			// FIXME: check that is is not a del or expire
			v <- get(evt.Node.Value)
		}
	}()
	return v
}

func followBootStrap() {
	cfg := etcd.Config{Endpoints: disc.Endpoints()}
	kAPI, err := proxy.NewKeysAPI(cfg)
	if err != nil {
		log.Warning(err)
		return
	}
	resp, err := kAPI.Get(RootContext, ProxyConfigKey, nil)
	if err != nil {
		log.Warning(err)
		return
	} else if resp.Node.Dir {
		log.WithFields(log.Fields{"key": resp.Node.Key}).Warning("not a valid node")
		return
	}
	doReloadbyConfig(get(resp.Node.Value))
}

func Follow() {
	followBootStrap() // bootstrap proxy config

	var c ctx.Context

	c, ConfigReset = ctx.WithCancel(RootContext)
	go func() {
		cfg := etcd.Config{Endpoints: disc.Endpoints()}
		watcher, err := proxy.NewWatcher(cfg, ProxyConfigKey, 0)
		if err != nil {
			log.Warning(err)
			return
		}
		for yay := true; yay; {
			v := doWatch(c, watcher)
			select {
			case <-c.Done():
				yay = false
			case proxyTargets, ok := <-v:
				if ok && len(proxyTargets) != 0 {
					go doReloadbyConfig(proxyTargets)
				}
				yay = ok
			}
		}
	}()
}