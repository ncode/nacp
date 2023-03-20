job "app" {

  group "app" {

    task "app" {

      meta {
        postgres = "native"
      }
      driver = "docker"

      config { # a very simple docker container
        image = "busybox:latest"
        command = "sh"
        args = ["-c", "while true; do echo \"hello @ $(date)\"; sleep 5; done"]
      }
    }
  }
}
