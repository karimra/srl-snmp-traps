package app

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"time"

	"context"

	g "github.com/gosnmp/gosnmp"
	"github.com/itchyny/gojq"
	"github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/gnmic/api"
	"github.com/openconfig/gnmic/formatters"
	log "github.com/sirupsen/logrus"
	"github.com/vishvananda/netns"
	"golang.org/x/sync/semaphore"
	"google.golang.org/protobuf/encoding/prototext"
)

const (
	sysUpTimeInstanceOID = "1.3.6.1.2.1.1.3.0"
)

func (a *app) StartSubscriptions(ctx context.Context) {
	opts := []api.GNMIOption{
		api.EncodingASCII(),
		api.SubscriptionListModeSTREAM(),
	}

	for _, p := range a.getTriggersPaths() {
		opts = append(opts,
			api.Subscription(
				api.SubscriptionModeON_CHANGE(),
				api.Path(p),
			),
		)
	}

	subscribeRequest, err := api.NewSubscribeRequest(opts...)
	if err != nil {
		log.Errorf("failed to create a subscription request: %v", err)
		return
	}

	log.Debugf("\n%s", prototext.Format(subscribeRequest))

SUB:
	nctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go a.tg.Subscribe(nctx, subscribeRequest, "traps")

	rspCh, errCh := a.tg.ReadSubscriptions()
	for {
		select {
		case rsp, ok := <-rspCh:
			if !ok {
				return
			}
			log.Debugf("got subscription notification: %v", rsp.Response)
			a.handleSubscribeResponse(ctx, rsp.Response)
		case err, ok := <-errCh:
			if !ok {
				return
			}
			if err != nil {
				log.Errorf("subscription failed: %v", err)
				cancel()
				goto SUB
			}
		}
	}
}

func (a *app) getTriggersPaths() []string {
	p := make([]string, 0, len(a.traps))
	for _, t := range a.traps {
		p = append(p, t.Trigger.Path)
	}
	return p
}

func (a *app) handleSubscribeResponse(ctx context.Context, rsp *gnmi.SubscribeResponse) {
	evs, err := formatters.ResponseToEventMsgs("", rsp, nil)
	if err != nil {
		log.Errorf("failed to convert subscribe response to event: %v", err)
		return
	}
	for _, t := range a.traps {
		for _, ev := range evs {
			if _, ok := ev.Values[t.Trigger.Path]; !ok {
				continue
			}
			log.Infof("trap %q matched ev: %v", t.Name, ev)
			// handle matched ev and trap
			err = a.handleTrapSend(ctx, t, ev)
			if err != nil {
				log.Errorf("failed to build and send trap: %v", err)
			}
		}
	}
}

func (a *app) handleTrapSend(ctx context.Context, t *trapDefinition, ev *formatters.EventMsg) error {
	pdus := make([]g.SnmpPDU, 0, len(t.TrapPDU.Bindings)+1)

	// append systemUptime pdu
	pdus = append(pdus, g.SnmpPDU{
		Name:  sysUpTimeInstanceOID,
		Type:  g.TimeTicks,
		Value: uint32(time.Since(a.startTime).Seconds()),
	})
	// run trigger publish
	varsVals, err := a.triggerPublish(t.Trigger, ev)
	if err != nil {
		return err
	}
	// fmt.Println("trigger published vars", varsVals)
	// varsVals := make([]any, 0, len(tpb))
	// varsVals = append(varsVals, tpb...)
	// run tasks
	// fmt.Println("trigger published vars", varsVals)
	// taskVars := make([]any, 0, len(t.Trigger.publishCode))
	for _, tsk := range t.Tasks {
		rs, err := tsk.run(ctx, a.tg, varsVals...)
		if err != nil {
			return err
		}
		// fmt.Printf("!! task %s var %#v\n", tsk.Name, rs)
		varsVals = append(varsVals, rs...)
	}
	//
	var trapCommunity string
	if t.TrapPDU.communityCode != nil {
		r, err := runJQ(t.TrapPDU.communityCode, nil, varsVals...)
		if err != nil {
			return err
		}
		var ok bool
		trapCommunity, ok = r.(string)
		if !ok {
			return fmt.Errorf("resulting community string is not a string: %v", r)
		}
	}
	// build trap PDU
	for _, bind := range t.TrapPDU.Bindings {
		oid, err := runJQ(bind.oidCode, nil, varsVals...)
		if err != nil {
			return err
		}
		val, err := runJQ(bind.valueCode, nil, varsVals...)
		if err != nil {
			return err
		}
		pdu := g.SnmpPDU{
			Name:  fmt.Sprintf("%s", oid), // TODO: double check
			Type:  pduType(bind.Type),
			Value: val,
		}
		pdus = append(pdus, pdu)
	}

	// send trap PDU
	trapPDU := g.SnmpTrap{
		Variables: pdus,
		IsInform:  t.TrapPDU.InformPDU,
	}
	// debug
	if log.GetLevel() > log.DebugLevel {
		b, _ := json.MarshalIndent(trapPDU.Variables, "", "  ")
		log.Printf("trapPDU variables:\n%s", string(b))
	}
	//
	a.sendTrap(trapPDU, trapCommunity)
	return nil
}

func (a *app) triggerPublish(t *trigger, ev *formatters.EventMsg) ([]any, error) {
	input := ev.ToMap()
	rs := make([]any, 0, len(t.publishCode))
	for _, mv := range t.publishCode {
		for _, c := range mv {
			r, err := runJQ(c, input)
			if err != nil {
				return nil, err
			}
			rs = append(rs, r)
		}
	}
	return rs, nil
}

func runJQ(code *gojq.Code, ev map[string]interface{}, vars ...any) (interface{}, error) {
	iter := code.Run(ev, vars...)
	for {
		r, ok := iter.Next()
		if !ok {
			break
		}
		switch r := r.(type) {
		case error:
			return nil, r
		default:
			return r, nil
		}
	}
	return nil, nil
}

func pduType(typ string) g.Asn1BER {
	switch typ {
	case "bool":
		return g.Boolean
	case "int":
		return g.Integer
	case "bitString":
		return g.BitString
	case "octetString":
		return g.OctetString
	case "null":
		return g.Null
	case "objectID":
		return g.ObjectIdentifier
	case "objectDescription":
		return g.ObjectDescription
	case "ipAddress":
		return g.IPAddress
	case "counter32":
		return g.Counter32
	case "gauge32":
		return g.Gauge32
	case "timeTicks":
		return g.TimeTicks
	case "opaque":
		return g.Opaque
	case "nsapAddress":
		return g.NsapAddress
	case "counter64":
		return g.Counter64
	case "uint32":
		return g.Uinteger32
	case "opaqueFloat":
		return g.OpaqueFloat
	case "opaqueDouble":
		return g.OpaqueDouble
	}
	return g.UnknownType
}

func (a *app) sendTrap(trapPDU g.SnmpTrap, trapCommunity string) {
	wg := new(sync.WaitGroup)
	sem := semaphore.NewWeighted(1)
	for _, dest := range a.config.destinations {
		if dest.AdminState != "enable" {
			continue
		}
		wg.Add(1)
		sem.Acquire(context.TODO(), 1)
		go func(dest *snmpTrapDestination) {
			defer wg.Done()
			defer sem.Release(1)
			// netwIns
			var netInstName string
			if netInst, ok := a.config.nwInst[dest.NetworkInstance]; ok {
				netInstName = fmt.Sprintf("%s-%s", netInst.BaseName, dest.NetworkInstance)
			} else {
				log.Errorf("unknown network instance name: %s", dest.NetworkInstance)
				return
			}
			n, err := netns.GetFromName(netInstName)
			if err != nil {
				log.Errorf("failed getting NS %q: %v", netInstName, err)
				return
			}
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			err = netns.Set(n)
			if err != nil {
				log.Infof("failed setting NS to %s: %v", n, err)
				return
			}
			// init client and send trap
			snmpClient := g.NewHandler()
			snmpClient.SetTarget(dest.ip)
			if trapCommunity != "" {
				snmpClient.SetCommunity(trapCommunity)
			} else {
				snmpClient.SetCommunity(dest.Community)
			}
			snmpClient.SetPort(dest.port)
			snmpClient.SetVersion(g.Version2c)
			err = snmpClient.Connect()
			if err != nil {
				log.Errorf("failed to connect to destination %q: %v", dest.Address, err)
				return
			}
			_, err = snmpClient.SendTrap(trapPDU)
			if err != nil {
				log.Errorf("failed to send trap to destination %q: %v", dest.Address, err)
				return
			}
		}(dest)
	}
	wg.Wait()
}
