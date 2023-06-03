package app

import (
	"context"

	"github.com/openconfig/gnmic/api"
	"github.com/openconfig/gnmic/formatters"
	"github.com/openconfig/gnmic/target"
)

func (tsk *task) run(ctx context.Context, tg *target.Target, vars ...any) ([]any, error) {
	var ev *formatters.EventMsg
	var err error
	if tsk.GNMI != nil {
		ev, err = tsk.runGNMI(ctx, tg, vars...)
		if err != nil {
			return nil, err
		}
	}
	input := ev.ToMap()
	rs := make([]any, 0, len(tsk.publishCode))
	for _, mv := range tsk.publishCode {
		for _, c := range mv {
			r, err := runJQ(c, input, vars...)
			if err != nil {
				return nil, err
			}
			rs = append(rs, r)
		}
	}
	return rs, nil
}

func (tsk *task) runGNMI(ctx context.Context, tg *target.Target, vars ...any) (*formatters.EventMsg, error) {
	opts := []api.GNMIOption{
		api.Encoding(tsk.GNMI.Encoding),
	}
	r, err := runJQ(tsk.GNMI.pathCode, nil, vars...)
	if err != nil {
		return nil, err
	}
	if rs, ok := r.(string); ok {
		opts = append(opts, api.Path(rs))
	}
	req, err := api.NewGetRequest(opts...)
	if err != nil {
		return nil, err
	}
	rsp, err := tg.Get(ctx, req)
	if err != nil {
		return nil, err
	}
	evs, err := formatters.GetResponseToEventMsgs(rsp, nil)
	if err != nil {
		return nil, err
	}
	if len(evs) == 0 {
		return nil, nil
	}
	return evs[0], nil
}
