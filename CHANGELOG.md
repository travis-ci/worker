## 1.4.0 (December 3rd, 2015)

FEATURES:

  * backend/docker: Allow disabling the CPU and RAM allocations by setting the
	config options to 0 (this was possible in previous versions, but was not
	documented or supported until now)

IMPROVEMENTS:

  * backend/gce: Send job ID and repository slug to image selector to help in
	log correlation
  * sentry: Only send fatal and panic levels to Sentry, with an option for
	sending errors as well (--sentry-hook-errors)
  * image/api\_selector: Send os:osx instead of the language when querying for
	an image matching an osx\_image flag

BUG FIXES:

  * amqp\_job: Send correct received\_at and started\_at timestamps to hub in
	the case of the job finishing before the received or started event is sent

## 1.3.0 (November 30th, 2015)

IMPROVEMENTS:

  * backend/docker: Tests added by @jacobgreenleaf :heart_eyes_cat:
  * utils/package-\*, bin/travis-worker-install: Rework and integration into
    base CI suite.
  * Switch to using `gvt` for dependency management.
  * backend/bluebox: **Removed**
  * amqp\_job: Send all known timestamps during state updates.
  * backend: Set defaults for all `StartAttributes` fields, which are also
    exposed via CLI.

BUG FIXES:

  * backend/docker: Use correct HostConfig when creating container
  * image/api\_selector: Set `is_default=true` when queries consist of a single
    search dimension.

## 1.2.0 (November 10th, 2015)

IMPROVEMENTS:

  * utils/pkg: Official releases built with go 1.5.1.
  * vendor/\*: Updated all vendored dependencies
  * utils/lintall: Set 1m deadline for all linters
  * backend/jupiterbrain: switch to env image selector
  * image/api:
    * Add image selection query with dist + group + language
    * Add last-ditch image selection query with `is_default=true`

FEATURES:

  * log-writer: Write folded worker info summary

BUG FIXES:

  * backend/gce: Removed wonky instance group feature
  * step/run-script: Do not requeue if max log length exceeded


## 1.1.1 (September 10th, 2015)

IMPROVEMENTS:

  * utils/pkg: updated upstart config to copy/run executable as
    `/var/tmp/run/$UPSTART_JOB`, allowing for multiple worker instances per
    host.

## 1.1.0 (September 9th, 2015)

FEATURES:

  * backend/gce:
    * Configurable image selector, defaulting to legacy selection
      method for backward compatibility.
    * Support for reading account JSON from filename or JSON blob.
    * Optionally add all instances to configurable instance group.
  * image/\*: New image selection abstraction with env-based and api-based
    implementations.

IMPROVEMENTS:

  * vendor/\*: Upgraded all vendored dependencies to latest.
  * utils/pkg:
    * Official releases built with go 1.5.
    * Packagecloud script altered to only use ruby stdlib dependencies,
      removing the need for bundler.
  * backend/gce: Lots of additional test coverage.
  * backend/\*: Introduction of `Setup` func for deferring mutative actions needed
    for runtime.
  * config: Addition of `Unset` method on `ProviderConfig`

BUG FIXES:

  * processor: Fix graceful shutdown by using `tryClose` on shutdown channel.

## 1.0.0 (August 19th, 2015)

FEATURES:

  * backend/gce: Add auto implode, which will cause a VM to automatically shut
    down after a hard timeout.

BUG FIXES:

  * logger: Make the processor= field in the logs not be empty anymore
  * sentry: Stringify the err field sent to Sentry, since it's usually parsed
    as a struct, making it just {} in Sentry.

## 0.7.0 (August 18th, 2015)

FEATURES:

  * backend: The local backend was added

IMPROVEMENTS:

  * backend/jupiterbrain: Add exponential backoff on all HTTP requests
  * sentry: Include stack trace in logs sent to Sentry
  * step/generate-script: Add exponential backoff to script generation

BUG FIXES:

  * backend/gce: Fix a bug causing VMs for a build language ending in symbols
    (such as C++) to error while booting
  * log-writer: Fix a race condition causing the log writer to be closed before
    the logs were fully flushed.
  * log-writer: Minimize locking in the internals of the log writer, making
    deadlocks less likely.
  * processor: Fix graceful and forceful shutdown when there are still build
    jobs waiting.

## 0.6.0 (July 23rd, 2015)

FEATURES:

  * backend: The GCE backend was added

IMPROVEMENTS:

  * step/upload-script: Add a timeout for the script upload (currently 1 minute)
  * step/upload-script: Treat connection errors as recoverable errors, and requeue the job
  * backend/jupiterbrain: Per-image boot time and count metrics

BUG FIXES:

  * backend/jupiterbrain: Fix a goroutine/memory leak where SSH connections for cancelled jobs wouldn't get cleaned up
  * logger: Don't print the job UUID if it's blank
  * processor: Fix a panic that would sometimes happen on graceful shutdown

## 0.5.2 (July 16th, 2015)

IMPROVEMENTS:

  * config: Use the server hostname by default if no Librato source is given
  * version: Only print the basename of the binary when showing version

BUG FIXES:

  * step/run-script: Print the log timeout and not the hard timeout in the log
    timeout error message [GH-49]

## 0.5.1 (July 14th, 2015)

FEATURES:

  * Runtime pool size management:  Send `SIGTTIN` and `SIGTTOU` signals to
    increase and decrease the pool size during runtime [GH-42]
  * Report runtime memory metrics, including GC pause times and rates, and
    goroutine count [GH-45]

IMPROVEMENTS:

  * Add more log messages so that all error messages are caught in some way

MISC:

  * Many smaller internal changes to remove all lint errors

## 0.5.0 (July 9th, 2015)

BACKWARDS INCOMPATIBILITIES:

  * backend: The Sauce Labs backend was removed [GH-36]

FEATURES:

  * backend: Blue Box backend was added [GH-32]
  * main: Lifecycle hooks were added [GH-33]
  * config: The log timeout can be set in the configuration
  * config: The log timeout and hard timeout can be set per-job in the payload
    from AMQP [GH-34]

## 0.4.4 (July 7th, 2015)

FEATURES:

  * backend/docker: Several new configuration settings:
    * `CPUS`: Number of CPUs available to each container (default is 2)
    * `MEMORY`: Amount of RAM available to each container (default is 4GiB)
    * `CMD`: Command to run when starting the container (default is /sbin/init)
  * backend/jupiter-brain: New configuration setting: `BOOT_POLL_SLEEP`, the
    time to wait between each poll to check if a VM has booted (default is 3s)
  * config: New configuration flag: `silence-metrics`, which will cause metrics
    not to be printed to the log even if no Librato credentials have been
    provided
  * main: `SIGUSR1` is caught and will cause each processor in the pool to print
    its current status to the logs

IMPROVEMENTS:

  * backend: Add `--help` messages for all backends
  * backend/docker: Container hostnames now begin with `travis-docker-` instead
    of `travis-go-`

BUG FIXES:

  * step/run-script: Format the timeout duration in the log timeout message as a
    duration instead of a float

## 0.4.3 (June 13th, 2015)

No code changes, but as of this release each Travis CI build will cause three
binaries to be uploaded: One for the commit SHA or tag, one for the branch and
one for the job number.

## 0.4.2 (June 13th, 2015)

IMPROVEMENTS:

  * backend/docker: Improve format of instance ID in the logs for each container

## 0.4.1 (June 13th, 2015)

BUG FIXES:

  * config: Include the `build-api-insecure-skip-verify` when writing the
    configuration using `--echo-config`

## 0.4.0 (June 13th, 2015)

FEATURES:

  * config: New flag: `build-api-insecure-skip-verify`, which will skip
    verifying the TLS certificate when requesting the build script

## 0.3.0 (June 11th, 2015)

FEATURES:

  * config: Hard timeout is now configurable using `HARD_TIMEOUT`
  * backend/docker: Allow for running containers in privileged mode using
    `TRAVIS_WORKER_DOCKER_PRIVILEGED=true`
  * main: `--help` will list configuration options

IMPROVEMENTS:

  * step/run-script: The instance ID is now printed in the "Using worker" line
    at the top of the job logs
  * backend/docker: Instead of just searching for images tagged with
    `travis:<language>`, also search for tags `<language>`, `travis:default` and
    `default`, in that order
  * step/upload-script: Requeue job immediately if a build script has been
    uploaded, which is a possible indication of a VM being reused

## 0.2.1 (June 11th, 2015)

FEATURES:

  * backend/jupiter-brain: More options available for image aliases. Now aliases
    named `<osx_image>`, `osx_image_<osx_image>`,
    `osx_image_<osx_image>_<language>`, `dist_<dist>_<language>`, `dist_<dist>`,
    `group_<group>_<language>`, `group_<group>`, `language_<language>` and
    `default_<os>` will be looked for, in that order.

IMPROVEMENTS:

  * logger: The logger always prints key=value formatted logs without colors
  * backend/jupiter-brain: Sleep in between requests to check if IP is available

## 0.2.0 (June 11th, 2015)

FEATURES:

  * backend: New provider: Jupiter Brain

IMPROVEMENTS:

  * backend/docker: CPUs that can be used by containers scales according to
    number of CPUs available on host
  * step/run-script: Print hostname and processor UUID at the top of the job log

## 0.1.0 (June 11th, 2015)

Initial release
