package server

// Bypass fixture 1 (packet M1-T4 R1): a package outside readonly imports client-go.
import _ "k8s.io/client-go/rest"
