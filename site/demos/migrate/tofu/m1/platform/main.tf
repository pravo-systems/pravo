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

data "terraform_remote_state" "net" {
  backend = "local"

  config = {
    path = "../net/terraform.tfstate"
  }
}

module "db" {
  source = "./modules/db"
}

resource "random_pet" "logs" {
  length = 2
  prefix = "logs"
}

resource "local_file" "web" {
  count    = 2
  filename = "out/web${count.index}-${count.index == 1 ? var.rev : "1"}.txt"
  content  = "web ${count.index}\n"

  lifecycle {
    create_before_destroy = true
  }
}

resource "null_resource" "gate" {
  triggers = { web = join(",", local_file.web[*].id) }

  provisioner "local-exec" {
    command = var.fail ? "exit 1" : "true"
  }
}
