package main

import (
	"bufio"
	"errors"
	"math/big"
	"os"
	"os/exec"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v2"
)

// DefaultNumNodes is the default number of nodes that should be spawned in
// a script.
const DefaultNumNodes = 8

// GenesisConfig represents a configuration for the genesis block of the network.
type GenesisConfig []struct {
	StartNode uint64 // The index of the first node to include in the allocation section

	EndNode uint64 // The index of the first node to no longer be included in the allocation section

	Amount *big.Int // The number of coins to allocate to each node in the block
}

// ScriptConfig represents an configuration for an individual setp in a .finkles.yaml file.
type ScriptConfig struct {
	Nodes *struct {
		N        uint64    // The number of nodes to spawn
		Args     *[]string // Any arguments that should be passed to the nodes
		Callback *string   // The name of a script that will be run each time a node has been spawned
	}
	DataDir *string   // A directory in which all of the SMCd data will be placed
	Steps   *[]string // Any commands that should be run after spawning the nodes
}

// State represents the state of the finkles coordinator
type State struct {
	Workers []exec.Cmd // Each of the nodes spawned by the script
}

// Start starts the script, but does not wait for it to finish.
func (cfg *ScriptConfig) Start() (*State, error) {
	// The number of nodes we'll spawn. If this value
	// has not been overridden by the configuration, use 8 as the default
	n := uint64(DefaultNumNodes)

	// If there is a node spawn config in the configuration, use that instead
	if cfg.Nodes != nil {
		n = cfg.Nodes.N
	}

	// Declare a state buffer that we can store worker info in
	var state State

	// Check if we are in a rust project directory
	_, err := os.Stat("cargo.toml")

	// If smcd isn't installed install it, use smcd instead
	if _, openErr := exec.LookPath("smcd"); openErr != nil || err == nil || !os.IsNotExist(err) {
		log.Info("Installing SMCd")

		// Install the smcd command
		err := exec.Command("cargo", "install", "--path", ".").Run()
		if err != nil {
			return nil, err
		}
	}

	// The multiaddr and peer ID of the network's bootstrap node
	var bootstrapNode []string

	log.WithFields(log.Fields{"n": n}).Info("Spawning a swarm of SummerCash nodes")

	// Spawn each of the nodes
	for i := uint64(0); i < n; i++ {
		// If i is less than the number of bootstrap nodes we need to make,
		// make this node a bootstrap node
		if bootstrapNode == nil {
			log.Info("Starting a bootstrap node for the swarm")

			// Start the bootstrap node
			cmd := exec.Command("smcd", "-n")

			// Logs from the bootstrap node
			output, err := cmd.StderrPipe()
			if err != nil {
				return nil, err
			}

			// Make a reader that we can use to analyze the output of the bp
			reader := bufio.NewReader(output)

			// Start the bootstrap node
			if err := cmd.Start(); err != nil {
				return nil, err
			}

			for {
				lineBytes, _, err := reader.ReadLine()
				if err != nil {
					return nil, err
				}

				// We want to work with this line as a string, since
				// the smcd cli only outputs human readable information
				line := string(lineBytes)

				// If this line is telling us what the peerID
				// of the bootstrap node is, store this in the
				// bp metadata var
				if strings.Contains(line, "peer ID") {
					bootstrapNode = append(bootstrapNode, strings.Split(line, "peer ID: ")[1])
				}

				// If we have not yet determined what the multiaddr of the bootstrap node is
				// and this node contains this information, store it in the bootstrap node
				// slice
				if strings.Contains(line, "Assigned to new address") && len(bootstrapNode) < 2 {
					bootstrapNode = append(bootstrapNode, strings.Split(strings.Split(line, "Assigned to new address; listening on ")[1], " now")[0])
				}

				// If we have determined what we need from the bootstrap node logs,
				// exit
				if len(bootstrapNode) >= 2 {
					break
				}
			}
		}

		// Use the network's bootstrap node
		//args = append(args, "--bootstrap-peer-id", bootstrapNode[0], "--bootstrap-peer-addr", bootstrapNode[1])

		//cmd := exec.Command("smcd")

		//state.Workers = append(state.Workers)
	}

	// Allow the caller to continue using the "state" of the command
	return &state, nil
}

// Config represents a configuration for the finkles command line utility.
type Config struct {
	ScriptConfig `yaml:",inline"` // A global configuration

	Test *ScriptConfig // A script used to test SMCd

	Spawn *ScriptConfig // A script used to deploy / spawn a node swarm
}

// readConfig reads a finkles configuration from the disk, considering a command line context.
func readConfig(c *cli.Context) (*Config, error) {
	// Read the configuration file
	file, err := os.Open(c.String("config"))
	if err != nil {
		// Make sure that the user knows no finkles config exists
		if strings.Contains(err.Error(), "no such file or directory") {
			return nil, errors.New("no finkles config found in the working directory")
		}

		return nil, err
	}
	defer file.Close()

	// The configuration file that the user has provided to us
	var cfg Config

	// Make a decoder so that we can take the configuration file and convert it
	// into structured data
	dec := yaml.NewDecoder(file)

	// Read from the file into the configuration buffer
	return &cfg, dec.Decode(&cfg)
}

func main() {
	// Finkles is a command line app build using Urfave's cli package
	app := cli.App{
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "config, c",
				Value: ".finkles.yaml",
				Usage: "Load configuration from `FILE`",
			},
		},
		Name:  "finkles",
		Usage: "deploy a new SummerCash network or manage an existing network by running alongside a data folder",
		Commands: []*cli.Command{
			{
				Name:    "spawn",
				Aliases: []string{"s"},
				Usage:   "spawns a SummerCash cluster from the provided configuration file",
				Action: func(c *cli.Context) error {
					// Read the configuration file from the disk
					cfg, err := readConfig(c)
					if err != nil {
						return err
					}

					// The script that will be run to spawn the nodes. Since "spawn" is a
					// generalized command, the global config, as well as the spawn config
					// can be used for this command
					script := cfg.Spawn

					if script == nil || script.Nodes == nil {
						// Use the config's global config
						script = &cfg.ScriptConfig
					}

					// Start the script
					_, err = script.Start()
					if err != nil {
						return err
					}

					return nil
				},
			},
			{
				Name:    "test",
				Aliases: []string{"t"},
				Usage:   "runs all SummerCash tests if cargo is available, and runs the `test` step contained in the .finkles.yaml file",
				Action: func(c *cli.Context) error {
					// REad the configuration file from the disk
					cfg, err := readConfig(c)
					if err != nil {
						return err
					}

					if cfg.Test == nil {
						return errors.New("configuration file does not contain script: 'test'")
					}

					return nil
				},
			},
		},
	}

	// Start the finkles command line interface, and log any errors to stderr
	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
