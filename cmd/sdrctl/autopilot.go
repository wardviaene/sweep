package main

import (
	"flag"
	"fmt"
	"strconv"
	"strings"
)

func init() {
	commands["autopilot"] = command{"autopilot [on|off] [-min-el 20] [-preempt[=false]] [-listen[=false]] [-record-iq[=false]] [-sats 25544,43017|all] | autopilot status | autopilot run <norad> | autopilot stop", cmdAutopilot}
}

func cmdAutopilot(a []string) error {
	if len(a) == 0 {
		return get("/api/autopilot", nil)
	}
	switch a[0] {
	case "status":
		return get("/api/autopilot", nil)
	case "stop":
		return post("/api/autopilot/stop", nil)
	case "run":
		if len(a) != 2 {
			return usageErr("autopilot")
		}
		n, err := strconv.Atoi(a[1])
		if err != nil || n <= 0 {
			return fmt.Errorf("autopilot run: bad NORAD id %q", a[1])
		}
		return post("/api/autopilot/run", map[string]any{"norad": n})
	}
	body := map[string]any{}
	if a[0] == "on" || a[0] == "off" {
		body["enabled"] = a[0] == "on"
		a = a[1:]
	}
	fs := flag.NewFlagSet("autopilot", flag.ContinueOnError)
	minEl := fs.Float64("min-el", 20, "minimum maximum elevation of a pass in degrees")
	preempt := fs.Bool("preempt", false, "stop a running survey, scanner or decoder for a pass (restored afterwards)")
	listen := fs.Bool("listen", true, "the VFO follows the downlink so you hear the pass")
	recIQ := fs.Bool("record-iq", false, "also record the raw IQ of each pass (large)")
	sats := fs.String("sats", "", "comma separated NORAD ids, or all")
	if err := fs.Parse(a); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErr("autopilot")
	}
	var perr error
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "min-el":
			body["min_elevation"] = *minEl
		case "preempt":
			body["preempt"] = *preempt
		case "listen":
			body["listen"] = *listen
		case "record-iq":
			body["record_iq"] = *recIQ
		case "sats":
			ids := []int{}
			if s := strings.TrimSpace(*sats); s != "" && s != "all" {
				for _, p := range strings.Split(s, ",") {
					n, err := strconv.Atoi(strings.TrimSpace(p))
					if err != nil || n <= 0 {
						perr = fmt.Errorf("-sats: bad NORAD id %q", p)
						return
					}
					ids = append(ids, n)
				}
			}
			body["satellites"] = ids
		}
	})
	if perr != nil {
		return perr
	}
	if len(body) == 0 {
		return get("/api/autopilot", nil)
	}
	return put("/api/autopilot", body)
}
