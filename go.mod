module github.com/prdroom/intuitik

go 1.27.3

// web/node_modules can hold third-party .go files; they are not part of this module.
ignore ./web/node_modules

require github.com/coder/websocket v1.8.15
