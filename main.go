package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/headzoo/surf/browser"
	. "github.com/logrusorgru/aurora"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh/terminal"
	"gopkg.in/headzoo/surf.v1"
)

var logger = log.New(os.Stdout, "", log.LUTC)

const (
	configFile = ".kubectl-login.json"
	timeout = time.Second * 120
)

type configuration struct {
	DexURL  string   `json:"dex-url"`
	Aliases []string `json:"aliases"`
}

type app struct {
	cluster   string
	namespace string
}


func main() {
	if err := cmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}
}

func getRawConfig() map[string]*configuration {
	configPath := os.Getenv("HOME") + string(os.PathSeparator) + configFile

	file, err := os.Open(configPath)
	if err != nil {
		logger.Fatalf("error: cannot open config file at %s: %v", configPath, err)
	}

	data, err := ioutil.ReadAll(file)
	if err != nil {
		closeFile(file)
		logger.Fatalf("error: cannot read config file at %s: %v", configPath, err)
	}

	var cfg map[string]*configuration
	err = json.Unmarshal(data, &cfg)
	if err != nil {
		closeFile(file)
		logger.Fatalf("error: cannot unmarshal contents of config file at %s: %v", configPath, err)
	}

	closeFile(file)
	return cfg
}

func getAlias(args []string) string {
	if len(args) == 0 {
		logger.Fatalf("Alias is mandatory i.e %s. try '%s' to get this value.",
			Bold(Cyan("kubectl-login <ALIAS>")), Bold(Cyan("cat $HOME/"+configFile)))
	}
	return args[0]
}

func closeFile(f *os.File) {
	if err := f.Close(); err != nil {
		logger.Printf("warning: couldn't close config file: %v", err)
	}
}

func getConfigByAlias(alias string, rawConfig map[string]*configuration) (*configuration, string) {
	for k, v := range rawConfig {
		if containsAlias(v, alias) {
			return v, k
		}
	}
	logger.Fatalf("Alias \"%s\" not found. Try '%s' to get this value.",
		Bold(Cyan(alias)), Bold(Cyan("cat $HOME/"+configFile)))
	return nil, ""
}

func containsAlias(c *configuration, s string) bool {
	for _, val := range c.Aliases {
		if val == s {
			return true
		}
	}
	return false
}

func cmd() *cobra.Command {
	var a app

	c := cobra.Command{
		Use:   "kubectl login [namespace]",
		Short: "Authenticates users against OIDC and writes the required kubeconfig.",
		Long:  "",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {

			rawConfig := getRawConfig()
			alias := getAlias(os.Args[1:])
			config, cluster := getConfigByAlias(alias, rawConfig)
			a.namespace = alias
			a.cluster = cluster
			timer := time.AfterFunc(timeout, func() {
				log.Printf("\nLogin timeout... exiting")
				os.Exit(0)
			})
			defer timer.Stop()
			a.switchContext()
			if isLoggedIn() {
				log.Printf("Logged in: %v", cluster)
				os.Exit(0)
			}
			return login(cluster, config.DexURL)
		},
	}
	return &c
}

func login(cluster string, url string) error {
	for {
		fmt.Printf("Logging in to cluster %s\n", cluster)
		reader := bufio.NewReader(os.Stdin)
		fmt.Printf("username: ")
		username, _ := reader.ReadString('\n')
		fmt.Printf("password: ")
		bytePassword, _ := terminal.ReadPassword(int(syscall.Stdin))
		password := string(bytePassword)
		fmt.Println("")
		bow := surf.NewBrowser()
		bow.SetAttribute(browser.SendReferer, true)
		bow.SetAttribute(browser.MetaRefreshHandling, true)
		bow.SetAttribute(browser.FollowRedirects, true)
		err := bow.Open(url)
		if err != nil {
			return err
		}
		// Submit login button
		fm, _ := bow.Form("form")
		err = fm.Submit()
		if err != nil {
			return err
		}
		// Log in to dex
		fm, _ = bow.Form("form")
		fm.Input("login", strings.TrimSpace(username))
		fm.Input("password", strings.TrimSpace(password))
		err = fm.Submit()
		if err != nil {
			return err
		}
		// check response
		if bow.StatusCode() != 200 {
			fmt.Println(strings.TrimSpace(bow.Body()))
			continue
		}
		// handle login error
		resp, _ := bow.Dom().Find("#login-error").Html()
		if resp != "" {
			fmt.Printf("%s\n\n", strings.TrimSpace(resp))
			continue
		}
		// 
		resp = bow.Dom().Find("#idMergeConfig").Text()
		if resp != "" {
			err := kubeMerge(strings.TrimSpace(resp))
			if err != nil {
				return err
			}
			log.Printf("Logged in: %v", cluster)
			return nil
		}
	}
}

func kubeMerge (config string) error {
	cmd := exec.Command("/bin/sh", "-c", config)
	err := cmd.Run()
	if err != nil {
		fmt.Println(err)
	}
	return nil
}

func isLoggedIn() bool {
	err := exec.Command("kubectl", "get", "namespace").Run()
	return err == nil
}

func (a *app) switchContext() {
	clusterArg := fmt.Sprintf("--cluster=%s", a.cluster)
	user := fmt.Sprintf("--user=%s", a.cluster)
	cmd := exec.Command("kubectl", "config", "set-context", a.cluster, user, clusterArg, "--namespace="+a.namespace)
	err := cmd.Run()
	if err != nil {
		logger.Fatalf("error: cannot set kubectl login context: %v", err)
	}

	cmd = exec.Command("kubectl", "config", "use-context", a.cluster)
	err = cmd.Run()
	if err != nil {
		logger.Fatalf("error: cannot switch to kubectl login context: %v", err)
	}
}