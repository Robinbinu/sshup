package config

import (
	"os"
	"os/user"
	"strconv"
	"strings"

	ssh_config "github.com/kevinburke/ssh_config"
)

type Host struct {
	Alias          string
	HostName       string
	User           string
	Port           int
	IdentityFiles  []string
	IdentitiesOnly bool
}

func Parse(path string) ([]Host, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	cfg, err := ssh_config.Decode(file)
	if err != nil {
		return nil, err
	}

	var hosts []Host
	for _, cfgHost := range cfg.Hosts {
		for _, pattern := range cfgHost.Patterns {
			alias := pattern.String()
			if strings.ContainsAny(alias, "*?!") {
				continue
			}

			hostName, err := cfg.Get(alias, "HostName")
			if err != nil {
				return nil, err
			}
			if hostName == "" {
				hostName = alias
			}

			hostUser, err := cfg.Get(alias, "User")
			if err != nil {
				return nil, err
			}
			if hostUser == "" {
				hostUser = currentUsername()
			}

			port := 22
			portValue, err := cfg.Get(alias, "Port")
			if err != nil {
				return nil, err
			}
			if parsedPort, err := strconv.Atoi(strings.TrimSpace(portValue)); err == nil && parsedPort > 0 {
				port = parsedPort
			}

			identityFiles, err := cfg.GetAll(alias, "IdentityFile")
			if err != nil {
				return nil, err
			}

			identitiesOnly, _ := cfg.Get(alias, "IdentitiesOnly")

			hosts = append(hosts, Host{
				Alias:          alias,
				HostName:       hostName,
				User:           hostUser,
				Port:           port,
				IdentityFiles:  identityFiles,
				IdentitiesOnly: strings.EqualFold(strings.TrimSpace(identitiesOnly), "yes"),
			})
		}
	}

	return hosts, nil
}

func currentUsername() string {
	currentUser, err := user.Current()
	if err != nil || currentUser.Username == "" {
		return "root"
	}

	return currentUser.Username
}
