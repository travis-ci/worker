// Package image contains logic for image selection logic.
//
// Worker supports two ways of selecting the image to use for a compute
// instance: An APISelector that talks to job-board
// (https://github.com/travis-ci/job-board) and an ENVSelector that gets the
// data from environment variables.
package image
