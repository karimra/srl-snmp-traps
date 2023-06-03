package app

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/itchyny/gojq"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

type trapDefinition struct {
	Name    string   `yaml:"name,omitempty"`
	Trigger *trigger `yaml:"trigger,omitempty"`
	Tasks   []*task  `yaml:"tasks,omitempty"`
	TrapPDU *trapPDU `yaml:"trap,omitempty"`
}

type trigger struct {
	Path    string              `yaml:"path,omitempty"`
	Publish []map[string]string `yaml:"publish,omitempty"`

	publishCode []map[string]*gojq.Code
}

type task struct {
	Name    string              `yaml:"name,omitempty"`
	GNMI    *gNMITask           `yaml:"gnmi,omitempty"`
	Publish []map[string]string `yaml:"publish,omitempty"`

	publishCode []map[string]*gojq.Code
}

type gNMITask struct {
	RPC      string `yaml:"rpc,omitempty"`
	Path     string `yaml:"path,omitempty"`
	Encoding string `yaml:"encoding,omitempty"`

	pathCode *gojq.Code
}

type trapPDU struct {
	InformPDU bool `yaml:"inform,omitempty"`
	Community string
	Bindings  []*binding

	communityCode *gojq.Code
}

type binding struct {
	OID   string
	Type  string
	Value string

	oidCode   *gojq.Code
	valueCode *gojq.Code
}

func (a *app) readTrapsDefinition() error {
	return filepath.WalkDir(a.trapDir,
		func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			ext := filepath.Ext(path)
			if ext != ".yaml" && ext != ".yml" {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			t := new(trapDefinition)
			err = yaml.Unmarshal(b, t)
			if err != nil {
				return err
			}
			log.Infof("read trap: %+v", t)
			if t.Name == "" {
				t.Name = strings.TrimSuffix(path, ext)
			}
			err = t.parseCode()
			if err != nil {
				return err
			}
			a.traps = append(a.traps, t)
			return nil
		})
}

func (t *trapDefinition) parseCode() error {
	if t.Trigger == nil {
		return fmt.Errorf("trap definition %q missing \"trigger\"", t.Name)
	}
	if t.Trigger.Path == "" {
		return fmt.Errorf("trap definition %q missing \"trigger.path\"", t.Name)
	}
	if t.TrapPDU == nil {
		return fmt.Errorf("trap definition %q missing trap PDU definition under \"trap\"", t.Name)
	}
	if len(t.TrapPDU.Bindings) == 0 {
		return fmt.Errorf("trap definition %q missing trap PDU bindings under \"trap.bindings\"", t.Name)
	}

	var err error
	err = t.Trigger.parseCode()
	if err != nil {
		return fmt.Errorf("trap definition %q trigger parse failed: %v", t.Name, err)
	}

	triggerVars := make([]string, 0, len(t.Trigger.Publish)+len(t.Tasks))
	for _, mk := range t.Trigger.Publish {
		for k := range mk {
			triggerVars = append(triggerVars, "$"+k)
		}
	}

	log.Debugf("trap definition %q: triggerVars: %v", t.Name, triggerVars)
	for idx, tsk := range t.Tasks {
		err = tsk.parseCode(triggerVars...)
		if err != nil {
			return fmt.Errorf("trap definition %q task index %d parse failed: %v", t.Name, idx, err)
		}
		for _, mk := range tsk.Publish {
			for k := range mk {
				triggerVars = append(triggerVars, "$"+k)
			}
		}
	}

	log.Debugf("trap definition %q: allVars: %v", t.Name, triggerVars)
	if t.TrapPDU.Community != "" {
		t.TrapPDU.communityCode, err = parseJQ(t.TrapPDU.Community, triggerVars...)
		if err != nil {
			return fmt.Errorf("trap definition %q community parse failed: %v", t.Name, err)
		}
	}

	for idx, binding := range t.TrapPDU.Bindings {
		err = binding.parseCode(triggerVars...)
		if err != nil {
			return fmt.Errorf("trap definition %q binding index %d parse failed: %v", t.Name, idx, err)
		}
	}
	return nil
}

func (tr *trigger) parseCode() error {
	tr.publishCode = make([]map[string]*gojq.Code, 0, len(tr.Publish))
	for _, mkv := range tr.Publish {
		for k, v := range mkv {
			c, err := parseJQ(v)
			if err != nil {
				return err
			}
			tr.publishCode = append(tr.publishCode, map[string]*gojq.Code{k: c})
		}
	}
	return nil
}

func (tsk *task) parseCode(prevTasks ...string) error {
	var err error
	if tsk.GNMI != nil {
		tsk.GNMI.pathCode, err = parseJQ(tsk.GNMI.Path, prevTasks...)
		if err != nil {
			return err
		}
	}
	tsk.publishCode = make([]map[string]*gojq.Code, 0, len(tsk.Publish))
	for _, mkv := range tsk.Publish {
		for k, v := range mkv {
			c, err := parseJQ(v, prevTasks...)
			if err != nil {
				return err
			}
			tsk.publishCode = append(tsk.publishCode, map[string]*gojq.Code{k: c})
		}
	}
	return nil
}

func (b *binding) parseCode(prevTasks ...string) error {
	var err error
	b.oidCode, err = parseJQ(b.OID, prevTasks...)
	if err != nil {
		return err
	}
	b.valueCode, err = parseJQ(b.Value, prevTasks...)
	return err
}

func parseJQ(code string, prevVars ...string) (*gojq.Code, error) {
	q, err := gojq.Parse(strings.TrimSpace(code))
	if err != nil {
		return nil, err
	}
	return gojq.Compile(q, gojq.WithVariables(prevVars))
}
