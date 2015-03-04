package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/mijia/gobuildweb/assets"
	"github.com/mijia/gobuildweb/loggers"
)

type TaskType int

const (
	// The Order is important
	kTaskBuildImages TaskType = iota
	kTaskBuildStyles
	kTaskBuildJavaScripts
	kTaskBinaryTest
	kTaskBuildBinary
	kTaskBinaryRestart
)

type AppShellTask struct {
	taskType TaskType
	module   string
}

type AppShell struct {
	binName      string
	args         []string
	isProduction bool
	taskChan     chan AppShellTask
	curError     error
	command      *exec.Cmd
}

func (app *AppShell) Run() error {
	app.isProduction = false
	go app.startRunner()
	app.executeTask(
		AppShellTask{kTaskBuildImages, ""},
		AppShellTask{kTaskBuildStyles, ""},
		AppShellTask{kTaskBuildJavaScripts, ""},
		AppShellTask{kTaskBuildBinary, ""},
	)
	return nil
}

func (app *AppShell) Dist() error {
	app.isProduction = true
	fmt.Println()
	loggers.Info("Creating distribution package for %v-%v",
		rootConfig.Package.Name, rootConfig.Package.Version)

	var err error
	if err = app.buildImages(""); err != nil {
		loggers.Error("Error when building images, %v", err)
	} else if err = app.buildStyles(""); err != nil {
		loggers.Error("Error when building stylesheets, %v", err)
	} else if err = app.buildJavaScripts(""); err != nil {
		loggers.Error("Error when building javascripts, %v", err)
	} else if err = app.binaryTest(""); err != nil {
		loggers.Error("You have failed test cases, %v", err)
	} else if err == nil {
		goOs, goArch := runtime.GOOS, runtime.GOARCH
		targets := append(rootConfig.Distribution.CrossTargets, [2]string{goOs, goArch})
		visited := make(map[string]struct{})
		for _, target := range targets {
			buildTarget := fmt.Sprintf("%s_%s", target[0], target[1])
			if _, ok := visited[buildTarget]; ok {
				continue
			}
			visited[buildTarget] = struct{}{}
			if err = app.buildBinary(target[:]...); err != nil {
				loggers.Error("Error when building binary for %v, %v", target, err)
			}
		}
	}
	// TODO package all the binary and static assets
	return err
}

func (app *AppShell) startRunner() {
	for task := range app.taskChan {
		switch task.taskType {
		case kTaskBuildImages:
			app.curError = app.buildImages(task.module)
		case kTaskBuildStyles:
			app.curError = app.buildStyles(task.module)
		case kTaskBuildJavaScripts:
			app.curError = app.buildJavaScripts(task.module)
		case kTaskBinaryTest:
			app.curError = app.binaryTest(task.module)
		case kTaskBuildBinary:
			app.curError = app.buildBinary()
		case kTaskBinaryRestart:
			if app.curError == nil {
				if err := app.kill(); err != nil {
					loggers.Error("App cannot be killed, maybe you should restart the gobuildweb: %v", err)
				} else {
					if err := app.start(); err != nil {
						loggers.Error("App cannot be started, maybe you should restart the gobuildweb: %v", err)
					}
				}
			} else {
				loggers.Warn("You have errors with current assets and binary, please fix that ...")
			}
			fmt.Println()
			loggers.Info("Waiting for the file changes ...")
		}
	}
}

func (app *AppShell) executeTask(tasks ...AppShellTask) {
	for _, task := range tasks {
		app.taskChan <- task
	}
	app.taskChan <- AppShellTask{kTaskBinaryRestart, ""}
}

func (app *AppShell) kill() error {
	if app.command != nil && (app.command.ProcessState == nil || !app.command.ProcessState.Exited()) {
		if runtime.GOOS == "windows" {
			if err := app.command.Process.Kill(); err != nil {
				return err
			}
		} else if err := app.command.Process.Signal(os.Interrupt); err != nil {
			return err
		}

		rootConfig.RLock()
		isGraceful := rootConfig.Package.IsGraceful
		rootConfig.RUnlock()

		if !isGraceful {
			// Wait for our process to die before we return or hard kill after 3 sec
			// when this is not a graceful server
			select {
			case <-time.After(3 * time.Second):
				if err := app.command.Process.Kill(); err != nil {
					loggers.Warn("failed to kill the app: %v", err)
				}
			}
		}
		app.command = nil
	}
	return nil
}

func (app *AppShell) start() error {
	app.command = exec.Command("./"+app.binName, app.args...)
	app.command.Stdout = os.Stdout
	app.command.Stderr = os.Stderr

	if err := app.command.Start(); err != nil {
		return err
	}
	loggers.Succ("App is starting, %v", app.command.Args)
	fmt.Println()
	go app.command.Wait()
	time.Sleep(500 * time.Millisecond)
	return nil
}

func (app *AppShell) buildAssetsTraverse(functor func(entry string) error) error {
	rootConfig.RLock()
	vendors := rootConfig.Assets.VendorSets
	entries := rootConfig.Assets.Entries
	rootConfig.RUnlock()
	for _, vendor := range vendors {
		if err := functor(vendor.Name); err != nil {
			return err
		}
	}
	for _, entry := range entries {
		if err := functor(entry.Name); err != nil {
			return err
		}
	}
	return nil
}

func (app *AppShell) buildImages(entry string) error {
	if entry == "" {
		if err := assets.ResetDir("public/images", true); err != nil {
			return err
		}
		return app.buildAssetsTraverse(app.buildImages)
	}

	rootConfig.RLock()
	defer rootConfig.RUnlock()
	return assets.ImageLibrary(*rootConfig.Assets, entry).Build(app.isProduction)
}

func (app *AppShell) buildStyles(entry string) error {
	if entry == "" {
		if err := assets.ResetDir("public/stylesheets", true); err != nil {
			return err
		}
		return app.buildAssetsTraverse(app.buildStyles)
	}

	rootConfig.RLock()
	defer rootConfig.RUnlock()
	return assets.StyleSheet(*rootConfig.Assets, entry).Build(app.isProduction)
}

func (app *AppShell) buildJavaScripts(entry string) error {
	if entry == "" {
		if err := assets.ResetDir("public/javascripts", true); err != nil {
			return err
		}
		return app.buildAssetsTraverse(app.buildJavaScripts)
	}

	rootConfig.RLock()
	defer rootConfig.RUnlock()
	return assets.JavaScript(*rootConfig.Assets, entry).Build(app.isProduction)
}

func (app *AppShell) binaryTest(module string) error {
	if module == "" {
		module = "."
	}
	testCmd := exec.Command("go", "test", module)
	testCmd.Stderr = os.Stderr
	testCmd.Stdout = os.Stdout
	if err := testCmd.Run(); err != nil {
		loggers.Error("Error when testing go modules[%s], %v", module, err)
		return err
	}
	loggers.Succ("Module[%s] Test passed: %v", module, testCmd.Args)
	return nil
}

func (app *AppShell) buildBinary(params ...string) error {
	goOs, goArch := runtime.GOOS, runtime.GOARCH
	if len(params) == 2 && (goOs != params[0] || goArch != params[1]) {
		goOs, goArch = params[0], params[1]
	}

	rootConfig.RLock()
	binName := fmt.Sprintf("%s-%s.%s.%s",
		rootConfig.Package.Name, rootConfig.Package.Version,
		goOs, goArch)
	var buildOpts []string
	if app.isProduction {
		buildOpts = make([]string, len(rootConfig.Distribution.BuildOpts))
		copy(buildOpts, rootConfig.Distribution.BuildOpts)
	} else {
		buildOpts = make([]string, len(rootConfig.Package.BuildOpts))
		copy(buildOpts, rootConfig.Package.BuildOpts)
	}
	rootConfig.RUnlock()

	env := []string{
		fmt.Sprintf("PATH=%s", os.Getenv("PATH")),
		fmt.Sprintf("GOOS=%s", goOs),
		fmt.Sprintf("GOARCH=%s", goArch),
		fmt.Sprintf("GOPATH=%s", os.Getenv("GOPATH")),
	}
	if goOs == "windows" {
		binName += ".exe"
	}

	flags := make([]string, 0, 3+len(buildOpts))
	flags = append(flags, "build")
	flags = append(flags, buildOpts...)
	flags = append(flags, []string{"-o", binName}...)
	buildCmd := exec.Command("go", flags...)
	buildCmd.Env = env
	loggers.Debug("Running build: %v", buildCmd.Args)
	start := time.Now()
	if output, err := buildCmd.CombinedOutput(); err != nil {
		loggers.Error("Building failed: %s", string(output))
		return err
	}
	app.binName = binName
	duration := float64(time.Since(start).Nanoseconds()) / 1e6
	loggers.Succ("Got binary built %s, takes=%.3fms", binName, duration)
	return nil
}

func NewAppShell(args []string) *AppShell {
	return &AppShell{
		args:     args,
		taskChan: make(chan AppShellTask),
	}
}
