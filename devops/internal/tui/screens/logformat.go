package screens

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"devops/internal/tui/core"
	"devops/internal/tui/theme"
)

// Log display formats, cycled with j in the log viewer.
var logFormats = []string{"readable", "json", "raw"}

var (
	levelKeys   = []string{"level", "severity", "lvl", "levelname", "log.level", "loglevel"}
	messageKeys = []string{"msg", "message", "event", "MESSAGE", "Message", "text"}
	errorKeys   = []string{"error", "err", "exception", "stack", "stack_trace", "stacktrace", "trace", "errorMessage"}
	timeKeys    = []string{"time", "timestamp", "ts", "@timestamp", "datetime", "date", "@t"}
)

// readable turns one log message into styled display lines:
//   - JSON objects become "LEVEL message  key=value …" with errors/stack
//     traces on their own indented lines;
//   - "prefix {json}" lines (Lambda, many loggers) get the same treatment;
//   - plain text keeps its line breaks and gets level colouring.
func readable(msg string) []string {
	start := strings.Index(msg, "{")
	if start >= 0 && strings.HasSuffix(strings.TrimSpace(msg), "}") {
		var obj map[string]any
		if json.Unmarshal([]byte(msg[start:]), &obj) == nil {
			return formatObject(strings.TrimSpace(msg[:start]), obj)
		}
	}
	var out []string
	for _, l := range strings.Split(msg, "\n") {
		out = append(out, core.ColorLine(strings.ReplaceAll(l, "\t", "  ")))
	}
	return out
}

func formatObject(prefix string, obj map[string]any) []string {
	take := func(keys []string) string {
		for _, k := range keys {
			if v, ok := obj[k]; ok {
				delete(obj, k)
				if s, ok := v.(string); ok {
					return s
				}
				b, _ := json.Marshal(v)
				return string(b)
			}
		}
		return ""
	}
	level := strings.ToUpper(take(levelKeys))
	message := take(messageKeys)
	errText := take(errorKeys)
	take(timeKeys) // the viewer already shows the event time
	if level == "" {
		// Lambda-style "2024-… <request id> ERROR" prefixes carry the level.
		for _, f := range strings.Fields(prefix) {
			if l := strings.ToUpper(f); l == "ERROR" || l == "WARN" || l == "WARNING" || l == "INFO" || l == "DEBUG" || l == "FATAL" {
				level = l
			}
		}
	}

	var fields []string
	flatten2("", obj, &fields)
	sort.Strings(fields)

	head := levelBadge(level)
	if message != "" {
		head += msgStyle(level).Render(message)
	} else if prefix != "" {
		head += theme.Body.Render(prefix)
	}
	if len(fields) > 0 {
		head += "  " + strings.Join(fields, " ")
	}
	out := []string{head}
	if errText != "" {
		for _, l := range strings.Split(errText, "\n") {
			out = append(out, theme.Bad.Render("  "+strings.ReplaceAll(l, "\t", "  ")))
		}
	}
	return out
}

func levelBadge(level string) string {
	switch level {
	case "":
		return ""
	case "ERROR", "FATAL", "CRITICAL", "PANIC", "ERR":
		return theme.Bad.Bold(true).Render(fmt.Sprintf("%-5s", level)) + " "
	case "WARN", "WARNING":
		return theme.Warn.Bold(true).Render("WARN ") + " "
	case "DEBUG", "TRACE":
		return theme.Dim.Render(fmt.Sprintf("%-5s", level)) + " "
	}
	return theme.Title.Render(fmt.Sprintf("%-5s", level)) + " "
}

func msgStyle(level string) interface{ Render(...string) string } {
	switch level {
	case "ERROR", "FATAL", "CRITICAL", "PANIC", "ERR":
		return theme.Bad
	case "WARN", "WARNING":
		return theme.Warn
	case "DEBUG", "TRACE":
		return theme.Dim
	}
	return theme.Strong
}

// flatten2 renders remaining fields as dim key=value pairs (nested keys dotted).
func flatten2(prefix string, v any, out *[]string) {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			flatten2(key, e, out)
		}
	case nil:
	default:
		var s string
		switch y := x.(type) {
		case string:
			s = y
			if strings.ContainsAny(s, " \t") {
				s = fmt.Sprintf("%q", s)
			}
		case []any:
			b, _ := json.Marshal(y)
			s = string(b)
		default:
			s = fmt.Sprint(y)
		}
		*out = append(*out, theme.Dim.Render(prefix+"=")+theme.Body.Render(s))
	}
}
