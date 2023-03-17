module github.com/travis-ci/worker

go 1.12

require (
	cloud.google.com/go v0.81.0
	contrib.go.opencensus.io/exporter/stackdriver v0.5.0
	github.com/Azure/go-ntlmssp v0.0.0-20180810175552-4a21cbd618b4 // indirect
	github.com/ChrisTrenkamp/goxpath v0.0.0-20170922090931-c385f95c6022 // indirect
	github.com/Jeffail/tunny v0.0.0-20180304204616-59cfa8fcb19f
	github.com/aws/aws-sdk-go v1.15.31
	github.com/bitly/go-simplejson v0.5.0
	github.com/cenk/backoff v2.1.0+incompatible
	github.com/containerd/containerd v1.6.1 // indirect
	github.com/docker/docker v20.10.13+incompatible
	github.com/docker/go-connections v0.4.0
	github.com/dustin/go-humanize v1.0.0
	github.com/dylanmei/iso8601 v0.1.0 // indirect
	github.com/flosch/pongo2 v0.0.0-20190505152737-8914e1cf9164 // indirect
	github.com/garyburd/redigo v0.0.0-20180404160726-569eae59ada9
	github.com/getsentry/raven-go v0.2.0
	github.com/gorilla/mux v1.7.3
	github.com/gorilla/websocket v1.5.0 // indirect
	github.com/jtacoma/uritemplates v1.0.0
	github.com/juju/clock v0.0.0-20190205081909-9c5c9712527c // indirect
	github.com/juju/go4 v0.0.0-20160222163258-40d72ab9641a // indirect
	github.com/juju/persistent-cookiejar v0.0.0-20171026135701-d5e5a8405ef9 // indirect
	github.com/juju/retry v0.0.0-20180821225755-9058e192b216 // indirect
	github.com/juju/utils v0.0.0-20180820210520-bf9cc5bdd62d // indirect
	github.com/juju/version v0.0.0-20180108022336-b64dbd566305 // indirect
	github.com/juju/webbrowser v0.0.0-20180907093207-efb9432b2bcb // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/lxc/lxd v0.0.0-20190613145114-3dac7136d553
	github.com/masterzen/azure-sdk-for-go v0.0.0-20161014135628-ee4f0065d00c // indirect
	github.com/masterzen/simplexml v0.0.0-20160608183007-4572e39b1ab9 // indirect
	github.com/masterzen/winrm v0.0.0-20180702085143-58761a495ca4
	github.com/mihasya/go-metrics-librato v0.0.0-20171227215858-c2a1624c7a80
	github.com/mitchellh/multistep v0.0.0-20170316185339-391576a156a5
	github.com/moby/term v0.0.0-20210619224110-3f7ff695adc6 // indirect
	github.com/nu7hatch/gouuid v0.0.0-20131221200532-179d4d0c4d8d // indirect
	github.com/packer-community/winrmcp v0.0.0-20180921211025-c76d91c1e7db
	github.com/pborman/uuid v1.2.0
	github.com/pkg/errors v0.9.1
	github.com/pkg/sftp v1.8.3
	github.com/rackspace/gophercloud v0.0.0-20161013232434-e00690e87603
	github.com/rcrowley/go-metrics v0.0.0-20180503174638-e2704e165165
	github.com/sirupsen/logrus v1.8.1
	github.com/streadway/amqp v0.0.0-20180806233856-70e15c650864
	github.com/stretchr/testify v1.7.0
	go.opencensus.io v0.23.0
	golang.org/x/crypto v0.1.0
	golang.org/x/net v0.7.0
	golang.org/x/oauth2 v0.0.0-20210819190943-2bc19b11175f
	golang.org/x/sync v0.0.0-20220722155255-886fb9371eb4
	google.golang.org/api v0.43.0
	gopkg.in/httprequest.v1 v1.2.0 // indirect
	gopkg.in/juju/environschema.v1 v1.0.0 // indirect
	gopkg.in/macaroon-bakery.v2 v2.1.0 // indirect
	gopkg.in/macaroon.v2 v2.1.0 // indirect
	gopkg.in/retry.v1 v1.0.3 // indirect
	gopkg.in/robfig/cron.v2 v2.0.0-20150107220207-be2e0b0deed5 // indirect
	gopkg.in/urfave/cli.v1 v1.20.0
)

replace github.com/go-check/check v1.0.0-20180628173108-788fd7840127 => github.com/go-check/check v0.0.0-20180628173108-788fd7840127

replace github.com/Sirupsen/logrus v0.0.0-20181010200618-458213699411 => github.com/Sirupsen/logrus v1.0.6
