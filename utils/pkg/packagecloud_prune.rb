#!/usr/bin/env ruby
# from http://blog.packagecloud.io/api/2015/07/06/pruning-packages-using-the-API/

require 'json'
require 'pp'
require 'rest-client'
require 'time'

if ENV['PACKAGECLOUD_TOKEN']
  API_TOKEN = ENV['PACKAGECLOUD_TOKEN']
else
  puts "Set PACKAGECLOUD_TOKEN"
  exit 1
end

if ENV['PC_USER']
  USER = ENV['PC_USER']
else
  puts "Set PC_USER"
  exit 1
end

MAX_ERRORS = Integer(ENV['MAX_ERRORS'] || 10)

if ARGV[0]
  REPOSITORY = ARGV[0]
else
  puts "Supply the repository name as the first argument"
  puts "E.g. 'worker' or 'worker-testing"
  exit 1
end

PACKAGE = 'travis-worker'
if ARGV[1]
  DIST = ARGV[1]
else
  puts "Supply the distribution name+release as the second argument"
  puts "E.g. 'ubuntu/trusty' or 'el/7'"
  exit 1
end

if ARGV[2]
  LIMIT = ARGV[2].to_i
else
  LIMIT = 10
end

if DIST.include?("ubuntu")
  PACKAGE_TYPE = "deb"
  PACKAGE_ARCH = "amd64"
elsif DIST.include?("el")
  PACKAGE_TYPE = "rpm"
  PACKAGE_ARCH = "x86_64"
end

base_url = "https://#{API_TOKEN}:@packagecloud.io/api/v1/repos/#{USER}/#{REPOSITORY}"

package_url = "/package/#{PACKAGE_TYPE}/#{DIST}/#{PACKAGE}/#{PACKAGE_ARCH}/versions.json"

url = base_url + package_url
package_versions = RestClient.get(url)

parsed_package_versions = JSON.parse(package_versions)

sorted_package_versions = parsed_package_versions.sort_by { |x| Time.parse(x["created_at"]) }.reverse!
i = sorted_package_versions.size - 1
puts "There are currently #{i} packages in #{REPOSITORY}"
puts "Your LIMIT is #{LIMIT}"

if i > LIMIT
  puts "Deleting #{i - LIMIT}"
else
  puts "The number of packages is below #{LIMIT}, so not yanking any."
  exit 0
end

n_errors = 0

until i == LIMIT
  to_yank = sorted_package_versions[i]

  distro_version = to_yank["distro_version"]
  filename = to_yank["filename"]
  yank_url = "/#{distro_version}/#{filename}"
  url = base_url + yank_url

  begin
    puts "attempting to yank #{filename}"
    result = RestClient.delete(url)
    p result
    if result == {} || result == '{}'
      puts "successfully yanked #{filename}!"
    else
      puts "failed with #{result}"
    end
    i -= 1
  rescue => e
    raise(e) if n_errors >= MAX_ERRORS
    puts "ERROR: #{e}"
    n_errors += 1
  end
end
