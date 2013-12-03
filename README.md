# About travis-worker #
[![Build Status](https://travis-ci.org/travis-ci/travis-worker.png)](https://travis-ci.org/travis-ci/travis-worker)

This is home for the next generation of Travis CI worker. It is a WIP and is still very rough around the edges for broader community of contributors to jump in.

## Running the worker

Kill the worker and VBox processes

    cd travis-worker/ && killall -9 -r java && sleep 3 && killall -9 -r VBox

Run the worker and sending stdout to log/worker.log (without wiping the old log):

    nohup bin/thor travis:worker:boot >> log/worker.log 2>&1 &

Run the worker with the Jolokia JMX web agent

    JAVA_OPTS=-javaagent:vendor/jolokia-1.0.5/jolokia-jvm-1.0.5-agent.jar=port=8088,host=localhost bin/thor travis:worker:boot


Run the worker with VisualVM support on a remote machine:

    JRUBY_OPTS="-J-Dcom.sun.management.jmxremote.port=1099 -J-Dcom.sun.management.jmxremote.authenticate=false -J-Dcom.sun.management.jmxremote.ssl=false -J-Djava.rmi.server.hostname=127.0.0.1" nohup bin/thor travis:worker:boot >> log/worker.log 2>&1 &

## Running the Thor console

    ruby -Ilib -rubygems lib/thor/console.rb

## Getting started ##

Install Bundler:

    gem install bundler

Pull down dependencies:

    bundle install --deployment --binstubs

## Running tests ##

On JRuby:

    bundle exec rake test

## Reporting Issues

Please report any issues on the [central Travis CI issue tracker](https://github.com/travis-ci/travis-ci/issues).

## License & copyright information ##

See LICENSE file.

Copyright (c) 2011-2012 [Travis CI development team](https://github.com/travis-ci).
