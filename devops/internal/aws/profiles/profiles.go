// Package profiles discovers profiles from the AWS shared config and
// credentials files without resolving any credentials.
package profiles

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Profile struct {
	Name    string
	Region  string
	SSO     bool
	RoleARN string
	Source  string // "config", "credentials" or "config+credentials"
}

func (p Profile) Kind() string {
	switch {
	case p.SSO:
		return "sso"
	case p.RoleARN != "":
		return "assume-role"
	case strings.Contains(p.Source, "credentials"):
		return "static keys"
	}
	return "config"
}

func ConfigPath() string {
	if v := os.Getenv("AWS_CONFIG_FILE"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".aws", "config")
}

func CredentialsPath() string {
	if v := os.Getenv("AWS_SHARED_CREDENTIALS_FILE"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".aws", "credentials")
}

// Discover lists configured profiles, "default" first.
func Discover() ([]Profile, error) {
	byName := map[string]*Profile{}
	add := func(name, src string) *Profile {
		p, ok := byName[name]
		if !ok {
			p = &Profile{Name: name, Source: src}
			byName[name] = p
		} else if !strings.Contains(p.Source, src) {
			p.Source += "+" + src
		}
		return p
	}

	err := parseINI(ConfigPath(), func(section string, kv map[string]string) {
		var name string
		switch {
		case section == "default":
			name = "default"
		case strings.HasPrefix(section, "profile "):
			name = strings.TrimSpace(strings.TrimPrefix(section, "profile "))
		default:
			return // sso-session, services, etc.
		}
		p := add(name, "config")
		p.Region = kv["region"]
		p.SSO = kv["sso_session"] != "" || kv["sso_start_url"] != ""
		p.RoleARN = kv["role_arn"]
	})
	if err != nil {
		return nil, err
	}
	if err := parseINI(CredentialsPath(), func(section string, _ map[string]string) {
		add(section, "credentials")
	}); err != nil {
		return nil, err
	}

	out := make([]Profile, 0, len(byName))
	for _, p := range byName {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == "default" || out[j].Name == "default" {
			return out[i].Name == "default"
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Initial picks the starting profile: explicit > AWS_PROFILE > default > first.
func Initial(list []Profile, preferred string) string {
	for _, c := range []string{preferred, os.Getenv("AWS_PROFILE"), "default"} {
		if c == "" {
			continue
		}
		for _, p := range list {
			if p.Name == c {
				return c
			}
		}
	}
	if len(list) > 0 {
		return list[0].Name
	}
	return "" // environment credentials only
}

func parseINI(path string, section func(name string, kv map[string]string)) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	var cur string
	kv := map[string]string{}
	flush := func() {
		if cur != "" {
			section(cur, kv)
		}
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush()
			cur = strings.TrimSpace(line[1 : len(line)-1])
			kv = map[string]string{}
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			kv[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	flush()
	return sc.Err()
}

// Regions is the selectable list of commercial regions.
var Regions = []string{
	"us-east-1", "us-east-2", "us-west-1", "us-west-2",
	"af-south-1",
	"ap-east-1", "ap-south-1", "ap-south-2", "ap-southeast-1", "ap-southeast-2", "ap-southeast-3", "ap-southeast-4", "ap-southeast-5", "ap-southeast-7",
	"ap-northeast-1", "ap-northeast-2", "ap-northeast-3",
	"ca-central-1", "ca-west-1",
	"eu-central-1", "eu-central-2", "eu-west-1", "eu-west-2", "eu-west-3", "eu-south-1", "eu-south-2", "eu-north-1",
	"il-central-1", "me-south-1", "me-central-1", "mx-central-1", "sa-east-1",
}
