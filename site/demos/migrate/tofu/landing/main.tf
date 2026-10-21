terraform {
  required_providers {
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
    local = {
      source  = "hashicorp/local"
      version = "~> 2.5"
    }
  }
}

resource "random_pet" "server" {
  length = 2
}

resource "local_file" "motd" {
  filename = "motd.txt"
  content  = "serving ${random_pet.server.id}\n"
}

resource "local_file" "note" {
  for_each = toset(["alpha", "beta"])
  filename = "notes/${each.key}.txt"
  content  = "${each.key}\n"
}
