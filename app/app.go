package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	agent "github.com/karimra/srl-ndk-demo"
	"github.com/nokia/srlinux-ndk-go/ndk"
	"github.com/openconfig/gnmic/api"
	"github.com/openconfig/gnmic/target"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/prototext"
)

const (
	retryInterval = 2 * time.Second
)
const (
	snmpTrapsDestinationPath = ".system.snmp-traps.destination"
	gnmiServerUnixSocket     = "unix:///opt/srlinux/var/run/sr_gnmi_server"
)

type app struct {
	config  *config
	debug   bool
	trapDir string

	agent *agent.Agent
	tuCh  chan *telemUpdate

	tg        *target.Target
	traps     []*trapDefinition
	startTime time.Time
}

type appOption func(*app)

func WithAgent(agt *agent.Agent) func(a *app) {
	return func(a *app) {
		a.agent = agt
	}
}

func WithDebug(d bool) func(a *app) {
	return func(a *app) {
		a.debug = d
	}
}

func WithTrapDir(dir string) func(a *app) {
	return func(a *app) {
		a.trapDir = dir
	}
}

func New(opts ...appOption) *app {
	a := &app{
		config: &config{
			m:            &sync.RWMutex{},
			destinations: map[string]*snmpTrapDestination{},
			trx:          map[string][]*ndk.ConfigNotification{},
			nwInst:       map[string]*ndk.NetworkInstanceData{},
		},
		debug:     false,
		agent:     &agent.Agent{},
		tuCh:      make(chan *telemUpdate),
		tg:        &target.Target{},
		traps:     make([]*trapDefinition, 0),
		startTime: time.Now(),
	}
	for _, opt := range opts {
		opt(a)
	}
READ:
	// walk trap dir
	err := a.readTrapsDefinition()
	if err != nil {
		log.Errorf("failed to read trap definitions: %v", err)
		time.Sleep(10 * time.Second)
		goto READ
	}
	log.Infof("read %d trap definition(s)", len(a.traps))
	return a
}

type config struct {
	m            *sync.RWMutex
	destinations map[string]*snmpTrapDestination
	trx          map[string][]*ndk.ConfigNotification
	nwInst       map[string]*ndk.NetworkInstanceData
}

type snmpTrapDestination struct {
	Address         string `json:"address,omitempty"`
	Community       string `json:"community,omitempty"`
	NetworkInstance string `json:"network-instance,omitempty"`
	AdminState      string `json:"admin-state,omitempty"`
	// OperState       string `json:"oper-state,omitempty"`

	ip   string
	port uint16
}

func (a *app) Run(ctx context.Context) {
	var err error
START:
	a.tg, err = api.NewTarget(
		api.Address(gnmiServerUnixSocket),
		api.Insecure(true),
		api.Timeout(retryInterval),
		// api.Username("admin"),
		// api.Password("NokiaSrl1!"),
	)
	if err != nil {
		log.Errorf("failed to create a gNMI client towards the nodes UDS address: %v", err)
		time.Sleep(retryInterval)
		goto START
	}
	err = a.tg.CreateGNMIClient(ctx)
	if err != nil {
		log.Errorf("failed to create a gNMI client towards the nodes UDS address: %v", err)
		time.Sleep(retryInterval)
		goto START
	}

	//
	cfgStream := a.agent.StartConfigNotificationStream(ctx)
	nwInstStream := a.agent.StartNwInstNotificationStream(ctx)
	go a.updateTelemetryCh(ctx)
	go a.StartSubscriptions(ctx)
	for {
		select {
		case nwInstEvent := <-nwInstStream:
			log.Debugf("NwInst notification: %+v", nwInstEvent)
			b, err := prototext.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(nwInstEvent)
			if err != nil {
				log.Errorf("NwInst notification Marshal failed: %+v", err)
				continue
			}
			log.Debugf("NwInst event JSON:\n%s", string(b))
			for _, ev := range nwInstEvent.GetNotification() {
				if nwInst := ev.GetNwInst(); nwInst != nil {
					a.handleNwInstCfg(ctx, nwInst)
					continue
				}
				log.Warnf("got empty nwInst, event: %+v", ev)
			}
		case event := <-cfgStream:
			log.Infof("Config notification: %+v", event)
			b, err := prototext.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(event)
			if err != nil {
				log.Infof("Config notification Marshal failed: %+v", err)
				continue
			}
			fmt.Printf("%s\n", string(b))
			for _, ev := range event.GetNotification() {
				if cfg := ev.GetConfig(); cfg != nil {
					a.handleConfigEvent(ctx, cfg)
					continue
				}
				log.Infof("got empty config, event: %+v", ev)
			}
		case <-ctx.Done():
			log.Infof("context done: %v", ctx.Err())
			return
		}
	}
}

func (a *app) handleNwInstCfg(ctx context.Context, nwInst *ndk.NetworkInstanceNotification) {
	key := nwInst.GetKey()
	if key == nil {
		return
	}
	a.config.m.Lock()
	defer a.config.m.Unlock()
	switch nwInst.Op {
	case ndk.SdkMgrOperation_Create:
		a.config.nwInst[key.InstName] = nwInst.Data
	case ndk.SdkMgrOperation_Update:
		a.config.nwInst[key.InstName] = nwInst.Data
	case ndk.SdkMgrOperation_Delete:
	}
}

func (a *app) handleConfigEvent(ctx context.Context, cfg *ndk.ConfigNotification) {
	log.Infof("handling cfg: %+v", cfg)
	// fmt.Printf("PATH: %s\n", cfg.GetKey().GetJsPath())
	// fmt.Printf("KEYS: %v\n", cfg.GetKey().GetKeys())
	// fmt.Printf("JSON:\n%s\n", cfg.GetData().GetJson())

	jsPath := cfg.GetKey().GetJsPath()
	// collect non commit.end config notifications
	if jsPath != ".commit.end" {
		if _, ok := a.config.trx[jsPath]; !ok {
			a.config.trx[jsPath] = make([]*ndk.ConfigNotification, 0)
		}
		a.config.trx[jsPath] = append(a.config.trx[jsPath], cfg)
		return
	}
	// when paths is ".commit.end", handle the stored config notifications
	log.Infof("handling config commits: %+v", a.config.trx)
	defer log.Infof("done handling commits")
	a.config.m.Lock()
	defer a.config.m.Unlock()

	// .system.snmp_traps.destination
	for _, txCfg := range a.config.trx[snmpTrapsDestinationPath] {
		switch txCfg.Op {
		case ndk.SdkMgrOperation_Create:
			a.handleCfgSnmpTrapDestinationCreate(ctx, txCfg)
		case ndk.SdkMgrOperation_Update:
			a.handleCfgSnmpTrapDestinationUpdate(ctx, txCfg)
		case ndk.SdkMgrOperation_Delete:
			a.handleCfgSnmpTrapDestinationDelete(ctx, txCfg)
		}
	}

	a.config.trx = make(map[string][]*ndk.ConfigNotification)
}

func (a *app) handleCfgSnmpTrapDestinationCreate(ctx context.Context, cfg *ndk.ConfigNotification) {
	key := cfg.GetKey().GetKeys()[0]
	destinationConfig := new(snmpTrapDestination)
	err := json.Unmarshal([]byte(cfg.GetData().GetJson()), destinationConfig)
	if err != nil {
		log.Errorf("failed to unmarshal config data from path %s: %v", cfg.Key.JsPath, err)
		return
	}
	log.Infof("got SNMP trap destination config: %#v", destinationConfig)
	if _, ok := a.config.nwInst[destinationConfig.NetworkInstance]; !ok {
		log.Errorf("unknown network-instance %s", destinationConfig.NetworkInstance)
		return
	}
	destinationConfig.Address = key
	// parse address
	host, port, err := net.SplitHostPort(key)
	if err != nil {
		log.Errorf("failed to parse SNMP destination address %q: %v", key, err)
		return
	}
	destinationConfig.ip = host
	p, err := strconv.Atoi(port)
	if err != nil {
		log.Errorf("failed to parse SNMP destination address %q: %v", key, err)
		return
	}
	destinationConfig.port = uint16(p)
	//
	a.config.destinations[destinationConfig.Address] = destinationConfig
	telemPath := fmt.Sprintf("%s{.address==\"%s\"}", snmpTrapsDestinationPath, key)
	log.Infof("updating telemetry data with %q : %#v", telemPath, destinationConfig)
	updateTelemetryCh(a.tuCh, telemPath, destinationConfig)
}

func (a *app) handleCfgSnmpTrapDestinationUpdate(ctx context.Context, cfg *ndk.ConfigNotification) {
	key := cfg.GetKey().GetKeys()[0]
	destinationConfig := new(snmpTrapDestination)
	err := json.Unmarshal([]byte(cfg.GetData().GetJson()), destinationConfig)
	if err != nil {
		log.Errorf("failed to unmarshal config data from path %s: %v", cfg.Key.JsPath, err)
		return
	}
	log.Infof("got SNMP trap destination config: %#v", destinationConfig)
	if _, ok := a.config.nwInst[destinationConfig.NetworkInstance]; !ok {
		log.Errorf("unknown network-instance %s", destinationConfig.NetworkInstance)
		return
	}
	destinationConfig.Address = key
	// parse address
	host, port, err := net.SplitHostPort(key)
	if err != nil {
		log.Errorf("failed to parse SNMP destination address %q: %v", key, err)
		return
	}
	destinationConfig.ip = host
	p, err := strconv.Atoi(port)
	if err != nil {
		log.Errorf("failed to parse SNMP destination address %q: %v", key, err)
		return
	}
	destinationConfig.port = uint16(p)

	a.config.destinations[destinationConfig.Address] = destinationConfig
	telemPath := fmt.Sprintf("%s{.address==\"%s\"}", snmpTrapsDestinationPath, key)
	log.Infof("updating telemetry data with %q : %#v", telemPath, destinationConfig)
	updateTelemetryCh(a.tuCh, telemPath, destinationConfig)
}

func (a *app) handleCfgSnmpTrapDestinationDelete(ctx context.Context, cfg *ndk.ConfigNotification) {
	key := cfg.GetKey().GetKeys()[0]
	delete(a.config.destinations, key)
	telemPath := fmt.Sprintf("%s{.name==\"%s\"}", snmpTrapsDestinationPath, key)
	a.deleteTelemetry(ctx, telemPath)
}
