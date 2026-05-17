// Smoke fixture for the docker-smoke CI job. Exercises BuildLocal +
// gRPC spawn for both Python and Go local plugins inside the published
// container images. Not loaded by any production code path.

plugin "dt_py" {
  source  = "./py_plugin"
  version = "local"
}

plugin "dt_go" {
  source  = "./go_plugin"
  version = "local"
}
