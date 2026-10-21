terraform {
  required_providers {
    local = { source = "hashicorp/local" }
  }
}

variable "dir" { type = string }
variable "names" { type = list(string) }

resource "local_file" "note" {
  for_each = toset(var.names)
  filename = "${var.dir}/${each.key}.txt"
  content  = "${each.key}\n"
}

output "paths" {
  value = [for n in var.names : local_file.note[n].filename]
}
