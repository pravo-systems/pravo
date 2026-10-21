terraform {
  required_providers {
    random = { source = "hashicorp/random", version = "~> 3.6" }
    local  = { source = "hashicorp/local", version = "~> 2.5" }
    null   = { source = "hashicorp/null", version = "~> 3.2" }
  }
}

variable "rev" {
  type    = string
  default = "1"
}

variable "fail" {
  type    = bool
  default = false
}

resource "random_pet" "server" {
  length = 2
  prefix = "acme-dev"
}

resource "local_file" "cfg" {
  filename = "out/cfg-${var.rev}.txt"
  content  = "cfg\n"

  lifecycle {
    create_before_destroy = true
  }
}

resource "null_resource" "gate" {
  triggers = { cfg = local_file.cfg.filename }

  provisioner "local-exec" {
    command = var.fail ? "exit 1" : "true"
  }
}
