package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/urfave/cli"
)

var (
	app = cli.NewApp()
)

func init() {
	app.Name = strings.Title(app.Name)
	app.Usage = "Docker Swarm, with Consul"
	app.Version = Version
	app.EnableBashCompletion = true

	app.Flags = []cli.Flag{
		fConsulCACert,
		fConsulCAPath,
		fConsulClientKey,
		fConsulClientCert,
		fConsulHTTPAddr,
		fConsulHTTPAuth,
		fConsulHTTPToken,
		fConsulHTTPSSL,
		fConsulHTTPSSLVerify,
		fConsulTLSServerName,
		fDockerAPIVersion,
		fDockerCertPath,
		fDockerHost,
		fDockerTLSVerify,
	}
}

func main() {
	cmdWorker := cli.Command{
		Name:     "worker",
		Usage:    "Swarm worker join, leveraging Consul",
		Category: "Docker Swarm",
		Action:   doSwarm,
		Flags: []cli.Flag{
			fConsulRaftRetryInterval,
			fConsulRaftRetryMax,
			fSwarmAdvertiseAddr,
			fSwarmAvailability,
			fSwarmDataPathAddr,
			fSwarmListenAddr,
		},
	}
	sort.Sort(cli.FlagsByName(cmdWorker.Flags))
	app.Commands = append(app.Commands, cmdWorker)

	cmdManager := cli.Command{
		Name:     "manager",
		Usage:    "Swarm manager init/join, leveraging Consul",
		Category: "Docker Swarm",
		Action:   doSwarm,
		Flags: []cli.Flag{
			fConsulRaftRetryInterval,
			fConsulRaftRetryMax,
			fSwarmAdvertiseAddr,
			fSwarmAutolock,
			fSwarmAvailability,
			fSwarmCertExpiry,
			fSwarmDataPathAddr,
			fSwarmDispatcherHeartbeat,
			fSwarmExternalCA,
			fSwarmForceNewCluster,
			fSwarmListenAddr,
			fSwarmMaxSnapshots,
			fSwarmSnapshotInterval,
			fSwarmTaskHistoryLimit,
		},
	}
	sort.Sort(cli.FlagsByName(cmdManager.Flags))
	app.Commands = append(app.Commands, cmdManager)

	sort.Sort(cli.CommandsByName(app.Commands))
	sort.Sort(cli.FlagsByName(app.Flags))

	app.RunAndExitOnError()
}

func doSwarm(c *cli.Context) error {
	consul, err := newConsulClient(c)
	if err != nil {
		return err
	}

	// make sure that consul cluster has boot-strapped
	raft, err := consul.Operator().RaftGetConfiguration(nil)
	if err != nil || (raft != nil && len(raft.Servers) == 0) {
		for i := 0; i < c.Int(fConsulRaftRetryMax.Name); i++ {
			time.Sleep(c.Duration(fConsulRaftRetryInterval.Name))
			raft, err = consul.Operator().RaftGetConfiguration(nil)
			if err == nil && raft != nil && len(raft.Servers) > 0 {
				break
			}
		}
	}
	if err != nil {
		return err
	} else if raft == nil || len(raft.Servers) == 0 {
		return errors.New("unable to read Consul Raft status")
	}

	docker, err := newDockerClient(c)
	if err != nil {
		return err
	}

	engine, err := docker.Info(context.Background())
	if err != nil {
		return err
	}
	if engine.Swarm.NodeID != "" {
		if node, _, e := docker.NodeInspectWithRaw(context.Background(), engine.Swarm.NodeID); e == nil {
			fmt.Fprintf(c.App.Writer, "%s\n", node.ID)
			return nil
		}
	}

	if c.Command.Name == "worker" {
		if e := swarmJoin(docker, consul, c); e != nil {
			return e
		}
	} else if c.Command.Name == "manager" {
		lk, le := consul.LockKey("docker/swarm/leader/.lock")
		if le != nil {
			return le
		}

		ll, le := lk.Lock(nil)

		defer func() {
			lk.Unlock()
			lk.Destroy()
		}()

		if le != nil {
			return le
		}

		lkey := "docker/swarm/leader"
		lkvp, _, lkerr := consul.KV().Get(lkey, nil)
		if lkerr != nil {
			return lkerr
		}

		if ll != nil && (lkvp == nil || len(lkvp.Value) == 0) {
			err = swarmInit(docker, consul, c)
		} else {
			err = swarmJoin(docker, consul, c)
		}
		if err != nil {
			return err
		}
	}

	engine, err = docker.Info(context.Background())
	if err != nil {
		return err
	}
	fmt.Fprintf(c.App.Writer, "%s\n", engine.Swarm.NodeID)

	return nil
}
