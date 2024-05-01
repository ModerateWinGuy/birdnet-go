# Changelog

All notable changes to this project will be documented in this file.

## [0.5.2] - 2024-05-01

### 🚀 Features

 - Prometheus metrics support, endpoint reports BirdNET detections and application Golang runtime metrics - contributed by @aster1sk
 - Disk management by old audio capture cleanup - contributed by @isZumpo

### 🐛 Bug Fixes

- *(analysis)* File analysis restored
- *(capture)* Improve audio buffer write function time keeping
- *(datastore)* Refactor datastore Get, Delete and Save methods for efficient transaction and error handling
- *(datastore)* Refactor GetClipsQualifyingForRemoval method in interfaces.go for improved input validation and error handling
- *(birdweather)* Improve handling of HTTP Responses in UploadSoundscape to prevent possible panics
- *(birdweather)* Fixed PCM to WAV encoding for soundscape uploads
- *(birdweather)* Increase HTTP timeout to 45 seconds
- *(utils)* Do not report root user as missing from audio group
- *(tests)* Refactor createDatabase function in interfaces_test.go for improved error handling

### 💄 Enhancement

- *(audio)* Print selected audio capture device on realtime mode startup
- *(startup)* Enhance realtime mode startup message with system details to help troubleshooting

### 🚜 Refactor

- *(conf)* Remove unused Context struct from internal/conf/context.go
- *(processor)* Update range filter action to handle error when getting probable species

### 🏗️ Building

- *(deps)* Bump golang.org/x/crypto from 0.21.0 to 0.22.0
- *(deps)* Bump google.golang.org/protobuf from 1.32.0 to 1.33.0
- *(deps)* Bump golang.org/x/net from 0.21.0 to 0.23.0
- *(go)* Bump Go version from 1.21.6 to 1.22.2 in go.mod
- *(deps)* Bump labstack echo version from 4.11.4 to 4.12.0
- *(deps)* Bump gorm.io/gorm from 1.25.9 to 1.25.10
- *(deps)* Bump github.com/gen2brain/malgo from 0.11.21 to 0.11.22

### ⚙️ Miscellaneous Tasks

- Fix linter errors

### Github

- *(workflow)* Add tensorflow dependencies to golangci-lint

## [0.5.1] - 2024-04-05

### 🚀 Features

- MQTT publishing support, contribution by @janvrska
- Location filter threshold is now configurable value under BirdNET node

### 🏗️ Building

- *(deps)* Bump gorm.io/gorm from 1.25.8 to 1.25.9

## [0.5.0] - 2024-03-30

### 🚀 Features

- Privacy filter to discard audio clips with human vocals
- Save all BirdNET prediction results into table Results
- *(audio)* Check user group membership on audio device open failure and print instructions for a fix
- *(docker)* Added support for multiplatform build
- *(conf)* New function to detect if running in container

### 🐛 Bug Fixes

- *(docker)* Install ca-certificates package in container image
- *(capture)* Set capture start to 5 seconds before detection instead of 4 seconds
- *(capture)* Increase audio capture length from 9 to 12 seconds
- *(rtsp)* Wait before restarting FFmpeg and update parent process on exit to prevent zombies

### 💄 Enhancement

- *(database)* Switched sqlite journalling to MEMORY mode and added database optimize on closing
- *(workflow)* Update GitHub Actions workflow to build and push Docker image to GHCR
- *(workflow)* Update Docker actions versions
- *(workflow)* Support multiplatform build with github actions
- *(docker)* Add ffmpeg to container image
- *(labels)* Add Greek (el) translations by @hoover67
- *(ui)* Improve spectrogram generation to enable lazy loading of images
- *(make)* Improve make file

### 🚜 Refactor

- Moved middleware code to new file
- Improved spectrogram generation
- Moved middleware code to new file
- *(database)* Move save to common interface and change it to use transaction
- *(analyser)* BirdNET Predict function and related types
- *(audio)* Stopped audio device is now simply started again instead of full audio context restart
- *(audio)* Disabled PulseAudio to prioritise audio capture to use ALSA on Linux
- *(audio)* Set audio backend to fixed value based on OS
- *(config)* Refactor RSTP config settings
- *(processor)* Increase dog bark filter scope to 15 minutes and fix log messages
- *(rtsp)* Improve FFmpeg process restarts and stopping on main process exit
- *(labels)* Update makefile to zip labels.zip during build and have label files available in internal/birdnet/labels to make it easier to contribute language updates
- *(audio)* Improve way start time of audio capture is calculated

### 📚 Documentation

- *(capture)* Add documentation to audiobuffer.go
- Add git-cliff for changelog management
- *(changelog)* Update git cliff config

### 🎨 Styling

- Remove old commented code
- *(docker)* Removed commented out code

### 🏗️ Building

- *(deps)* Add zip to build image during build
- *(deps)* Bump gorm.io/driver/mysql from 1.5.4 to 1.5.6
- *(deps)* Bump gorm.io/gorm from 1.25.7 to 1.25.8
- *(makefile)* Update makefile
- *(makefile)* Fix tensorflow lite lib install step
- *(makefile)* Fix tflite install

### ⚙️ Miscellaneous Tasks

- *(assets)* Upgrade htmx to 1.9.11

### Github

- *(workflow)* Add windows build workflow
- *(workflow)* Updated windows build workflow
- *(workflow)* Add go lint workflow
- *(workflow)* Remove obsole workflows
- *(workflow)* Add build and attach to release workflow
- *(workflow)* Update release-build.yml to trigger workflow on edited releases

## [0.3.0] - 2023-11-04

### 🚀 Features

- Added directory command
- Config file support
- Config file support

### 🐛 Bug Fixes

- Estimated time remaining print fixed
- Start and end time fix for stdout

<!-- generated by git-cliff -->
