package cmd

import (
	"context"
	"time"

	"gitea.com/gitea/act_runner/client"
	"gitea.com/gitea/act_runner/engine"
	"gitea.com/gitea/act_runner/poller"
	"gitea.com/gitea/act_runner/runtime"

	"github.com/joho/godotenv"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

func runDaemon(ctx context.Context, task *runtime.Task) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		log.Infoln("Starting runner daemon")

		_ = godotenv.Load(task.Input.EnvFile)
		cfg, err := fromEnviron()
		if err != nil {
			log.WithError(err).
				Fatalln("invalid configuration")
		}

		initLogging(cfg)

		// try to connect to docker daemon
		// if failed, exit with error
		if err := engine.Start(ctx); err != nil {
			log.WithError(err).Fatalln("failed to connect docker daemon engine")
		}

		cli := client.New(
			cfg.Client.Address,
			cfg.Client.Secret,
			cfg.Client.SkipVerify,
			client.WithGRPC(cfg.Client.GRPC),
			client.WithGRPCWeb(cfg.Client.GRPCWeb),
		)

		for {
			err := cli.Ping(ctx, cfg.Runner.Name)
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			if ctx.Err() != nil {
				break
			}
			if err != nil {
				log.WithError(err).
					Errorln("cannot ping the remote server")
				// TODO: if ping failed, retry or exit
				time.Sleep(time.Second)
			} else {
				log.Infoln("successfully pinged the remote server")
				break
			}
		}

		var g errgroup.Group

		runner := &runtime.Runner{
			Client:  cli,
			Machine: cfg.Runner.Name,
			Environ: cfg.Runner.Environ,
		}

		poller := poller.New(
			cli,
			runner.Run,
			&client.Filter{
				Kind:     runtime.Kind,
				Type:     runtime.Type,
				OS:       cfg.Platform.OS,
				Arch:     cfg.Platform.Arch,
				Capacity: cfg.Runner.Capacity,
			},
		)

		g.Go(func() error {
			log.WithField("capacity", cfg.Runner.Capacity).
				WithField("endpoint", cfg.Client.Address).
				WithField("os", cfg.Platform.OS).
				WithField("arch", cfg.Platform.Arch).
				Infoln("polling the remote server")

			return poller.Poll(ctx, cfg.Runner.Capacity)
		})

		err = g.Wait()
		if err != nil {
			log.WithError(err).
				Errorln("shutting down the server")
		}
		return err
	}
}