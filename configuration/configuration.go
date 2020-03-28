// Copyright 2019 HAProxy Technologies
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package configuration

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"math/rand"
	"time"

	"github.com/oklog/ulid"
	"gopkg.in/yaml.v2"
)

var cfg *Configuration

type HAProxyConfiguration struct {
	ConfigFile      string `short:"c" long:"config-file" description:"Path to the haproxy configuration file" default:"/etc/haproxy/haproxy.cfg"`
	Userlist        string `short:"u" long:"userlist" description:"Userlist in HAProxy configuration to use for API Basic Authentication" default:"controller"`
	HAProxy         string `short:"b" long:"haproxy-bin" description:"Path to the haproxy binary file" default:"haproxy"`
	ReloadDelay     int    `short:"d" long:"reload-delay" description:"Minimum delay between two reloads (in s)" default:"5"`
	ReloadCmd       string `short:"r" long:"reload-cmd" description:"Reload command"`
	RestartCmd      string `short:"s" long:"restart-cmd" description:"Restart command"`
	ReloadRetention int    `long:"reload-retention" description:"Reload retention in days, every older reload id will be deleted" default:"1"`
	TransactionDir  string `short:"t" long:"transaction-dir" description:"Path to the transaction directory" default:"/tmp/haproxy"`
	BackupsNumber   int    `short:"n" long:"backups-number" description:"Number of backup configuration files you want to keep, stored in the config dir with version number suffix" default:"0"`
	MasterRuntime   string `short:"m" long:"master-runtime" description:"Path to the master Runtime API socket"`
	ShowSystemInfo  bool   `short:"i" long:"show-system-info" description:"Show system info on info endpoint"`
	GitMode         bool   `short:"g" long:"git-mode" description:"Run dataplaneapi in git mode, without running the haproxy and ability to push to Git"`
	GitSettingsFile string `long:"git-settings-file" description:"Path to the git settings file" default:"/etc/haproxy/git.settings"`
	DataplaneConfig string `short:"f" description:"Path to the dataplane configuration file" default:"" yaml:"-"`
}

type LoggingOptions struct {
	LogTo     string `long:"log-to" description:"Log target, can be stdout or file" default:"stdout" choice:"stdout" choice:"file"`
	LogFile   string `long:"log-file" description:"Location of the log file" default:"/var/log/dataplaneapi/dataplaneapi.log"`
	LogLevel  string `long:"log-level" description:"Logging level" default:"warning" choice:"trace" choice:"debug" choice:"info" choice:"warning" choice:"error"`
	LogFormat string `long:"log-format" description:"Logging format" default:"text" choice:"text" choice:"JSON"`
}

type ClusterConfiguration struct {
	ID                 AtomicString `yaml:"id"`
	Mode               AtomicString `yaml:"mode" default:"single"`
	BootstrapKey       AtomicString `yaml:"bootstrap_key"`
	ActiveBootstrapKey AtomicString `yaml:"active_bootstrap_key"`
	Token              AtomicString `yaml:"token"`
	URL                AtomicString `yaml:"url"`
	Port               AtomicString `yaml:"port"`
	APIBasePath        AtomicString `yaml:"api_base_path"`
	CertificatePath    AtomicString `yaml:"tls-certificate"`
	CertificateKeyPath AtomicString `yaml:"tls-key"`
	CertFetched        AtomicBool   `yaml:"cert_fetched"`
	Name               AtomicString `yaml:"name"`
	Status             AtomicString `yaml:"status"`
	Description        AtomicString `yaml:"description"`
}

type ServerConfiguration struct {
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	APIBasePath string `yaml:"api_base_path"`
}

type NotifyConfiguration struct {
	keyChanged chan struct{} `yaml:"-"`
	restart    chan struct{} `yaml:"-"`
}

type Configuration struct {
	HAProxy HAProxyConfiguration `yaml:"-"`
	Logging LoggingOptions       `yaml:"-"`
	Cluster ClusterConfiguration `yaml:"cluster"`
	Server  ServerConfiguration  `yaml:"-"`
	Notify  NotifyConfiguration  `yaml:"-"`
}

//Get retuns pointer to configuration
func Get() *Configuration {

	if cfg == nil {
		cfg = &Configuration{}
		cfg.Notify.keyChanged = make(chan struct{}, 1)
		cfg.Notify.restart = make(chan struct{}, 1)
	}
	return cfg
}

func (c *Configuration) GetBotstrapKeyChange() <-chan struct{} {
	return c.Notify.keyChanged
}

func (c *Configuration) BotstrapKeyChanged(bootstrapKey string) {
	c.Cluster.BootstrapKey.Store(bootstrapKey)
	err := c.Save()
	if err != nil {
		log.Println(err)
	}
	c.Notify.keyChanged <- struct{}{}
}

func (c *Configuration) BotstrapKeyReload() {
	c.Notify.keyChanged <- struct{}{}
}

func (c *Configuration) GetRestartNotification() <-chan struct{} {
	return c.Notify.restart
}

func (c *Configuration) RestartServer() {
	c.Notify.restart <- struct{}{}
}

func (c *Configuration) Load(swaggerJSON json.RawMessage, host string, port int) error {
	var m map[string]interface{}
	err := json.Unmarshal(swaggerJSON, &m)
	if err != nil {
		return err
	}
	cfg.Server.APIBasePath = m["basePath"].(string)
	if host == "localhost" {
		host = "127.0.0.1"
	}
	cfg.Server.Host = host
	cfg.Server.Port = port

	cfgLoaded := &Configuration{}
	if c.HAProxy.DataplaneConfig != "" {
		yamlFile, err := ioutil.ReadFile(c.HAProxy.DataplaneConfig)
		if err == nil {
			err = yaml.Unmarshal(yamlFile, cfgLoaded)
			if err != nil {
				log.Fatalf("Unmarshal: %v", err)
			}
		}
	}
	c.Cluster = cfgLoaded.Cluster

	if c.Cluster.Mode.Load() == "" {
		c.Cluster.Mode.Store("single")
	}
	if c.Cluster.CertificatePath.Load() == "" {
		c.Cluster.CertificatePath.Store("tls.crt")
	}
	if c.Cluster.CertificateKeyPath.Load() == "" {
		c.Cluster.CertificateKeyPath.Store("tls.key")
	}

	t := time.Now()
	entropy := ulid.Monotonic(rand.New(rand.NewSource(t.UnixNano())), 0)
	id := ulid.MustNew(ulid.Timestamp(t), entropy)

	if c.Cluster.Name.Load() == "" {
		c.Cluster.Name.Store(id.String())
	}

	return nil
}

func (c *Configuration) Save() error {
	if c.HAProxy.DataplaneConfig == "" {
		return nil
	}

	data, err := yaml.Marshal(&c)
	if err != nil {
		log.Fatalf("error: %v", err)
	}

	err = ioutil.WriteFile(c.HAProxy.DataplaneConfig, data, 0644)
	if err != nil {
		return err
	}
	return nil
}