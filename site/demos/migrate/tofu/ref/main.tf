terraform {
  required_providers {
    random = { source = "hashicorp/random", version = "~> 3.6" }
  }
}

data "terraform_remote_state" "net" {
  backend = "local"

  config = {
    path = "../net/terraform.tfstate"
  }
}

module "net" {
  source = "./modules/net"
}

resource "random_pet" "server" {
  length = 2
}

resource "random_pet" "node" {
  count  = 2
  length = 2
}

output "name" {
  value = random_pet.server.id
}

output "token" {
  value     = "t-${random_pet.server.id}"
  sensitive = true
}
