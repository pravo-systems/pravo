terraform {
  required_providers {
    local = { source = "hashicorp/local", version = "~> 2.5" }
  }
}

data "terraform_remote_state" "platform" {
  backend = "local"

  config = {
    path = "../platform/terraform.tfstate"
  }
}

resource "local_file" "upstreams" {
  filename = "out/upstreams.conf"
  content  = "${join("\n", data.terraform_remote_state.platform.outputs.endpoints)}\n"
}
