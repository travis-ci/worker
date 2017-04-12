# Change Log

The format is based on [Keep a Changelog](http://keepachangelog.com/).

## [Unreleased]
### Added
### Changed
### Deprecated
### Removed
### Fixed
### Security

## [2.8.0] - 2017-04-12
### Added
- amqp-job: include a state message counter in messages sent to hub
- backend/docker: mount a tmpfs as /run and make it executable, fixing [travis-ci/travis-ci#7062](https://github.com/travis-ci/travis-ci/issues/7062)
- backend/docker: support configurable SHM size, default to 64 MiB
- build-script-generator: include job ID in requests parameters to travis-build
- metrics: add a metric for when a job is finished, without including state name
- sentry: send the current version string to Sentry when reporting errors

### Changed
- amqp-job: send all known timestamps to hub on each state update, including queued-at
- build: support and build using Go 1.8.1

### Fixed
- amqp-canceller: fix error occurring if a job was requeued on the same worker before the previous instance had completely finished, causing cancellations to break
- amqp-job: fix a panic that could occur during shutdown due to an AMQP connection issue
- ssh: update the SSH library, pulling in the fix for [golang/go#18861](https://github.com/golang/go/issues/18861)

## [2.7.0] - 2017-02-08
### Added
- backend: add "SSH dial timeout" to all backends, with a default of 5 seconds, configurable with `SSH_DIAL_TIMEOUT` backend setting
- backend/docker: make the command to run the build script configurable with `BACKEND_DOCKER_EXEC_CMD` env var, default to `bash /home/travis/build.sh`
- backend/gce: make it configurable whether to give a booted instance a "public" IP with `BACKEND_GCE_PUBLIC_IP`, defaults to `true`
- backend/gce: make it configurable whether to connect to an instance's "public" IP with `BACKEND_GCE_PUBLIC_IP_CONNECT`, defaults to `true`
- log when a job is finished, including its "finishing state" (passed, failed, errored, etc.)
- log when a job is requeued

### Changed
- backend/docker: change default command run in Docker native mode from `bash -l /home/travis/build.sh` back to `bash /home/travis/build.sh`, reverting the change made in 2.6.0

## [2.6.2] - 2017-01-31
### Security
- backend/gce: remove service account from booted instances

### Added
- HTTP queue type, including implementations of `JobQueue`, `Job`, and
  `LogWriter`

### Changed
- build_script_generator: accepts `Job` instead of `*simplejson.Json`

### Fixed
- log_writer: pass timeout on creation and start timer on first write

## [2.6.1] - 2017-01-23
### Fixed
- processor: open log writer early, prevent panic

## [2.6.0] - 2017-01-17
### Security
- update to go `v1.7.4`

### Added
- cli: log processor pool total on shutdown
- amqp_job: meter for job finish state (#184)
- amqp_job: track queue time via queued_at field from payload
- amqp_job_queue: log job id immediately after json decode
- capture every requeue and send the error to sentry

### Changed
- image/api_selector: selection of candidates by multiple groups
- amqp_canceller: change verbosity of canceller missing job id to debug
- docker: SIGINT `STOPSIGNAL` for graceful shutdown

### Fixed
- processor: always mark job as done when Run finishes
- processor: use errors.Cause when checking error values (error job on log limit reached and similar conditions)
- backend/jupiterbrain: parse SSH key on backend init (#206)
- backend/jupiterbrain: add sleep between creating and wait-for-ip
- backend/docker: run bash with `-l` (login shell) in docker native mode
- image/api_selector: check job-board response status code on image selection
- image/api_selector: check tagsets for trailing commas before querying job-board
- amqp_job_queue: handle context cancellation when delivering build job
- ssh: request a 80x40 PTY

## [2.5.0] - 2016-10-03
### Added
- support for heartbeat URL checks a la [legacy
  worker](https://github.com/travis-ci/travis-worker/blob/4ca25dd/lib/travis/worker/application/http_heart.rb)
- max build log length is now configurable
- alpine-based Docker image
- added runtime-configurable script hooks.

### Changed
- check flags and env vars for start and stop hooks

### Fixed
- Handling `"false"` as a valid boolean false-y value
- logging for start/stop hooks is clearer now
- logging for jupiter-brain boot timeouts is more verbose
- AMQP connections get cleaned up as part of graceful shutdowns

## [2.4.0] - 2016-09-08
### Added
- backend/cloudbrain
- backend/docker: "native" script upload and execution option
- backend/gce: Support for instances without public IPs
- handle `SIGWINCH` by gracefully shutting down processor pool and pausing

### Changed
- (really) static binaries built on go 1.7.1, cross compiled natively
- step/run-script: Add a new line with a link to the docs explaining
  `travis_wait` when there's a log timeout error.
- switch to [keepachangelog](http://keepachangelog.com) format
- upgrade all dependencies
- backend/jupiterbrain: improved error logging via `pkg/errors`

### Removed
- Using [goxc](github.com/laher/goxc) for cross-compilation
- amqp-job: logging channel struct

### Fixed
- Handling `"false"` as a valid boolean false-y value

## [2.3.1] - 2016-05-30
### Changed
- backend/gce: Allow the per-job hard timeout sent from scheduler to be
  longer than the global GCE hard timeout.

### Fixed
- package/rpm: Fix the systemd service file so the Worker can start.

## [2.3.0] - 2016-04-12
### Added
- backend/gce: Add support for separate project ID for images via
  `IMAGE_PROJECT_ID` env var.
- amqp-job: Add support for custom amqp TLS certificates via `--amqp-tls-cert`
  and `--amqp-tls-cert-path` options.

### Fixed
- backend/gce: Requeue jobs on preempted instances (instances preemptively
  shutdown by gce).

## [2.2.0] - 2016-02-01
### Added
- cli: add config option (`--amqp-insecure`) to connect to AMQP without verifying TLS certificates

### Changed
- backend/gce: switched out rate limiter with Redis-backed rate limiter
- backend/gce: create a pseudo-terminal that's 80 wide by 40 high instead of 40 wide by 80 high
- backend/gce: make using preemptible instances configurable
- build: switch to Go 1.5.2 for Travis CI builds

## [2.1.0] - 2015-12-15
### Added
- backend/gce: Add support for a "premium" VM type.
- cli: Show current status and last job id via `SIGUSR1`.
- amqp-job: Track metrics for (started - received) duration.
- cli: Add more percentiles to Librato config.

### Changed
- backend/gce: Use [`multistep`](https://github.com/mitchellh/multistep)
  during instance creation and deletion.
- backend/gce: Allow configuration of script upload and startup timeouts.
- backend/gce: Various improvements to minimize API usage:
  - Exponential backoff while stopping instance.
  - Add option to skip API polling after initial instance delete call.
  - Add metrics to track API polling for instance readiness.
  - Cache the instance IP address as soon as it is available.
  - Add ticker-based rate limiting.
- backend/gce: Generate SSH key pair internally.
- config: DRY up the relationship between config struct & cli flags.

### Fixed
- backend/jupiterbrain: Ensure image name is present in instance line of
  worker summary.
- backend/docker: Pass resource limitations in `HostConfig`.
- backend/docker: Prioritize language tags over default image.

## [2.0.0] - 2015-12-09
### Added
- step/open-log-writer: Display revision URL and startup time in worker info
  header.
- Use [goxc](github.com/laher/goxc) for cross-compilation, constrained to
  `linux amd64` and `darwin amd64`.

### Changed
- backend/gce: Tweaks to minimize API calls, including:
  - Add a sleep interval for before first zone operation check.
  - Break out of instance readiness when `RUNNING` *or* `DONE`, depending
  on script upload SSH connectivity for true instance readiness.
  - Increase default script upload retries to 120.
  - Decrease default script upload sleep interval to 1s.

### Removed
- backend/gce: Remove legacy image selection method and configuration.

### Fixed
- step/run-script: Mark jobs as `errored` when log limit is exceeded,
  preventing infinite requeue.

## [1.4.0] - 2015-12-03
### Added
- backend/docker: Allow disabling the CPU and RAM allocations by setting the
	config options to 0 (this was possible in previous versions, but was not
	documented or supported until now)

### Changed
- backend/gce: Send job ID and repository slug to image selector to help in
	log correlation
- sentry: Only send fatal and panic levels to Sentry, with an option for
	sending errors as well (--sentry-hook-errors)
- image/api\_selector: Send os:osx instead of the language when querying for
	an image matching an osx\_image flag

### Fixed
- amqp\_job: Send correct received\_at and started\_at timestamps to hub in
	the case of the job finishing before the received or started event is sent

## [1.3.0] - 2015-11-30
### Added
- backend/docker: Tests added by @jacobgreenleaf :heart_eyes_cat:

### Changed
- utils/package-\*, bin/travis-worker-install: Rework and integration into
  base CI suite.
- Switch to using `gvt` for dependency management.
- amqp\_job: Send all known timestamps during state updates.
- backend: Set defaults for all `StartAttributes` fields, which are also
  exposed via CLI.

### Removed
- backend/bluebox

### Fixed
- backend/docker: Use correct `HostConfig` when creating container
- image/api\_selector: Set `is_default=true` when queries consist of a single
  search dimension.

## [1.2.0] - 2015-11-10
### Added
- image/api:
  - Add image selection query with dist + group + language
  - Add last-ditch image selection query with `is_default=true`
- log-writer: Write folded worker info summary

### Changed
- utils/pkg: Official releases built with go 1.5.1.
- vendor/\*: Updated all vendored dependencies
- utils/lintall: Set 1m deadline for all linters
- backend/jupiterbrain: switch to env image selector

### Fixed
- backend/gce: Removed wonky instance group feature
- step/run-script: Do not requeue if max log length exceeded

## [1.1.1] - 2015-09-10
### Changed
- utils/pkg: updated upstart config to copy/run executable as
  `/var/tmp/run/$UPSTART_JOB`, allowing for multiple worker instances per
  host.

## [1.1.0] - 2015-09-09
### Added
- backend/gce:
  - Configurable image selector, defaulting to legacy selection
    method for backward compatibility.
  - Support for reading account JSON from filename or JSON blob.
  - Optionally add all instances to configurable instance group.
- image/\*: New image selection abstraction with env-based and api-based
  implementations.

### Changed
- vendor/\*: Upgraded all vendored dependencies to latest.
- utils/pkg:
  - Official releases built with go 1.5.
  - Packagecloud script altered to only use ruby stdlib dependencies,
    removing the need for bundler.
- backend/gce: Lots of additional test coverage.
- backend/\*: Introduction of `Setup` func for deferring mutative actions needed
  for runtime.
- config: Addition of `Unset` method on `ProviderConfig`

### Fixed
- processor: Fix graceful shutdown by using `tryClose` on shutdown channel.

## [1.0.0] - 2015-08-19
### Added
- backend/gce: Add auto implode, which will cause a VM to automatically shut
  down after a hard timeout.

### Fixed
- logger: Make the processor= field in the logs not be empty anymore
- sentry: Stringify the err field sent to Sentry, since it's usually parsed
  as a struct, making it just {} in Sentry.

## [0.7.0] - 2015-08-18
### Added
- backend/local

### Changed
- backend/jupiterbrain: Add exponential backoff on all HTTP requests
- sentry: Include stack trace in logs sent to Sentry
- step/generate-script: Add exponential backoff to script generation

### Fixed
- backend/gce: Fix a bug causing VMs for a build language ending in symbols
  (such as C++) to error while booting
- log-writer: Fix a race condition causing the log writer to be closed before
  the logs were fully flushed.
- log-writer: Minimize locking in the internals of the log writer, making
  deadlocks less likely.
- processor: Fix graceful and forceful shutdown when there are still build
  jobs waiting.

## [0.6.0] - 2015-07-23
### Added
- backend/gce
- backend/jupiterbrain: Per-image boot time and count metrics
- step/upload-script: Add a timeout for the script upload (currently 1 minute)

### Changed
- step/upload-script: Treat connection errors as recoverable errors, and requeue the job

### Fixed
- backend/jupiterbrain: Fix a goroutine/memory leak where SSH connections for
  cancelled jobs wouldn't get cleaned up
- logger: Don't print the job UUID if it's blank
- processor: Fix a panic that would sometimes happen on graceful shutdown

## [0.5.2] - 2015-07-16
### Changed
- config: Use the server hostname by default if no Librato source is given
- version: Only print the basename of the binary when showing version

### Fixed
- step/run-script: Print the log timeout and not the hard timeout in the log
  timeout error message [GH-49]

## [0.5.1] - 2015-07-14
### Added
- Runtime pool size management:  Send `SIGTTIN` and `SIGTTOU` signals to
  increase and decrease the pool size during runtime [GH-42]
- Report runtime memory metrics, including GC pause times and rates, and
  goroutine count [GH-45]
- Add more log messages so that all error messages are caught in some way

### Changed
- Many smaller internal changes to remove all lint errors

## [0.5.0] - 2015-07-09
### Added
- backend/bluebox: (See #32)
- main: Lifecycle hooks (See #33)
- config: The log timeout can be set in the configuration
- config: The log timeout and hard timeout can be set per-job in the payload
  from AMQP (See #34)

### Removed
- backend/saucelabs: (See #36)

## [0.4.4] - 2015-07-07
### Added
- backend/docker: Several new configuration settings:
  - `CPUS`: Number of CPUs available to each container (default is 2)
  - `MEMORY`: Amount of RAM available to each container (default is 4GiB)
  - `CMD`: Command to run when starting the container (default is /sbin/init)
- backend/jupiter-brain: New configuration setting: `BOOT_POLL_SLEEP`, the
  time to wait between each poll to check if a VM has booted (default is 3s)
- config: New configuration flag: `silence-metrics`, which will cause metrics
  not to be printed to the log even if no Librato credentials have been
  provided
- main: `SIGUSR1` is caught and will cause each processor in the pool to print
  its current status to the logs
- backend: Add `--help` messages for all backends

### Changed
- backend/docker: Container hostnames now begin with `travis-docker-` instead
  of `travis-go-`

### Fixed
- step/run-script: Format the timeout duration in the log timeout message as a
  duration instead of a float

## [0.4.3] - 2015-06-13
### Added
- each Travis CI build will cause three binaries to be uploaded: One for the
  commit SHA or tag, one for the branch and one for the job number.

## [0.4.2] - 2015-06-13
### Changed
- backend/docker: Improve format of instance ID in the logs for each container

## [0.4.1] - 2015-06-13
### Fixed
- config: Include the `build-api-insecure-skip-verify` when writing the
  configuration using `--echo-config`

## [0.4.0] - 2015-06-13
### Added
- config: New flag: `build-api-insecure-skip-verify`, which will skip
  verifying the TLS certificate when requesting the build script

## [0.3.0] - 2015-06-11
### Changed
- config: Hard timeout is now configurable using `HARD_TIMEOUT`
- backend/docker: Allow for running containers in privileged mode using
  `TRAVIS_WORKER_DOCKER_PRIVILEGED=true`
- main: `--help` will list configuration options
- step/run-script: The instance ID is now printed in the "Using worker" line
  at the top of the job logs
- backend/docker: Instead of just searching for images tagged with
  `travis:<language>`, also search for tags `<language>`, `travis:default` and
  `default`, in that order
- step/upload-script: Requeue job immediately if a build script has been
  uploaded, which is a possible indication of a VM being reused

## [0.2.1] - 2015-06-11
### Changed
- backend/jupiter-brain: More options available for image aliases. Now aliases
  named `<osx_image>`, `osx_image_<osx_image>`,
  `osx_image_<osx_image>_<language>`, `dist_<dist>_<language>`, `dist_<dist>`,
  `group_<group>_<language>`, `group_<group>`, `language_<language>` and
  `default_<os>` will be looked for, in that order.
- logger: The logger always prints key=value formatted logs without colors
- backend/jupiter-brain: Sleep in between requests to check if IP is available

## [0.2.0] - 2015-06-11
### Added
- backend/jupiter-brain

### Changed
- backend/docker: CPUs that can be used by containers scales according to
  number of CPUs available on host
- step/run-script: Print hostname and processor UUID at the top of the job log

## 0.1.0 - 2015-06-11
### Added
- Initial release

[Unreleased]: https://github.com/travis-ci/worker/compare/v2.8.0...HEAD
[2.8.0]: https://github.com/travis-ci/worker/compare/v2.7.0...v2.8.0
[2.7.0]: https://github.com/travis-ci/worker/compare/v2.6.2...v2.7.0
[2.6.2]: https://github.com/travis-ci/worker/compare/v2.6.1...v2.6.2
[2.6.1]: https://github.com/travis-ci/worker/compare/v2.6.0...v2.6.1
[2.6.0]: https://github.com/travis-ci/worker/compare/v2.5.0...v2.6.0
[2.5.0]: https://github.com/travis-ci/worker/compare/v2.4.0...v2.5.0
[2.4.0]: https://github.com/travis-ci/worker/compare/v2.3.1...v2.4.0
[2.3.1]: https://github.com/travis-ci/worker/compare/v2.3.0...v2.3.1
[2.3.0]: https://github.com/travis-ci/worker/compare/v2.2.0...v2.3.0
[2.2.0]: https://github.com/travis-ci/worker/compare/v2.1.0...v2.2.0
[2.1.0]: https://github.com/travis-ci/worker/compare/v2.0.0...v2.1.0
[2.0.0]: https://github.com/travis-ci/worker/compare/v1.4.0...v2.0.0
[1.4.0]: https://github.com/travis-ci/worker/compare/v1.3.0...v1.4.0
[1.3.0]: https://github.com/travis-ci/worker/compare/v1.2.0...v1.3.0
[1.2.0]: https://github.com/travis-ci/worker/compare/v1.1.1...v1.2.0
[1.1.1]: https://github.com/travis-ci/worker/compare/v1.1.0...v1.1.1
[1.1.0]: https://github.com/travis-ci/worker/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/travis-ci/worker/compare/v0.7.0...v1.0.0
[0.7.0]: https://github.com/travis-ci/worker/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/travis-ci/worker/compare/v0.5.2...v0.6.0
[0.5.2]: https://github.com/travis-ci/worker/compare/v0.5.1...v0.5.2
[0.5.1]: https://github.com/travis-ci/worker/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/travis-ci/worker/compare/v0.4.4...v0.5.0
[0.4.4]: https://github.com/travis-ci/worker/compare/v0.4.3...v0.4.4
[0.4.3]: https://github.com/travis-ci/worker/compare/v0.4.2...v0.4.3
[0.4.2]: https://github.com/travis-ci/worker/compare/v0.4.1...v0.4.2
[0.4.1]: https://github.com/travis-ci/worker/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/travis-ci/worker/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/travis-ci/worker/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/travis-ci/worker/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/travis-ci/worker/compare/v0.1.0...v0.2.0
