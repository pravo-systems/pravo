terraform {
  required_providers {
    random = { source = "hashicorp/random", version = "~> 3.6" }
  }
}

resource "random_pet" "vpc" {
  length = 2
  prefix = "vpc"
}

output "vpc" {
  value = random_pet.vpc.id
}
