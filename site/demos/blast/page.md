---
title: Blast radius scenes
description: A certificate estate of 39 resources, built from two modules and three comprehensions. Four changes show which nodes a plan reaches and which it skips.
capture: true
normalize:
  - pattern: '(?m)^Downloading .*\n'
    replace: ""
  - pattern: '\[txn [0-9TZ]+-[0-9a-f]+\]'
    replace: '[txn <id>]'
---

This page is not published. `tools/capture` runs it, and `site/data/blast.json` records its output for the blast radius demo, which the page "What a plan reaches" embeds. `site/demos/blast/derive.go` lays the graph out and builds the four scenes from that record. Every file and every command below is what the band shows.

The estate is a small certificate authority. One root key signs three zone certificates. Each zone key signs the certificates of two services. The providers are `tls` and `local`, so nothing needs credentials.

A fence attribute `scene=` names the scene that a block belongs to. `tools/capture` ignores it; `derive.go` reads it.

## The project

The module name, the three packages and the specification.

```cue file=cue.mod/module.cue scene=base
module: "example.com/estate@v0"
language: version: "v0.14.0"
```

```cue file=pki/root.cue scene=base
package pki

import (
	loc "pravo.dev/providers/hashicorp/local"
	tlsp "pravo.dev/providers/hashicorp/tls"
)

#Root: {
	tls:   tlsp.#Provider
	local: loc.#Provider

	key: tls.#Resource.tls_private_key & {
		algorithm:   "ECDSA"
		ecdsa_curve: "P256"
	}
	cert: tls.#Resource.tls_self_signed_cert & {
		private_key_pem: key.private_key_pem
		subject: [{common_name: "estate root"}]
		is_ca_certificate:     true
		validity_period_hours: 87600
		allowed_uses: ["cert_signing"]
	}
	file: local.#Resource.local_file & {
		filename: "out/root.pem"
		content:  cert.cert_pem
	}
}
```

```cue file=pki/zone.cue scene=base
package pki

import (
	loc "pravo.dev/providers/hashicorp/local"
	tlsp "pravo.dev/providers/hashicorp/tls"
)

#Zone: {
	tls:         tlsp.#Provider
	local:       loc.#Provider
	name:        string
	issuer_key:  string
	issuer_cert: string

	key: tls.#Resource.tls_private_key & {
		algorithm: "RSA"
		rsa_bits:  4096
	}
	csr: tls.#Resource.tls_cert_request & {
		private_key_pem: key.private_key_pem
		subject: [{common_name: "\(name).estate"}]
	}
	cert: tls.#Resource.tls_locally_signed_cert & {
		cert_request_pem:      csr.cert_request_pem
		ca_private_key_pem:    issuer_key
		ca_cert_pem:           issuer_cert
		is_ca_certificate:     true
		validity_period_hours: 43800
		allowed_uses: ["cert_signing"]
	}
	file: local.#Resource.local_file & {
		filename: "out/\(name)/zone.pem"
		content:  cert.cert_pem
	}
}
```

```cue file=pki/service.cue scene=base
package pki

import (
	loc "pravo.dev/providers/hashicorp/local"
	tlsp "pravo.dev/providers/hashicorp/tls"
)

#Service: {
	tls:           tlsp.#Provider
	local:         loc.#Provider
	zone_name:     string
	name:          string
	renewal_hours: int
	issuer_key:    string
	issuer_cert:   string

	key: tls.#Resource.tls_private_key & {
		algorithm:   "ECDSA"
		ecdsa_curve: "P256"
	}
	csr: tls.#Resource.tls_cert_request & {
		private_key_pem: key.private_key_pem
		subject: [{common_name: "\(name).\(zone_name).estate"}]
		dns_names: ["\(name).\(zone_name).estate"]
	}
	cert: tls.#Resource.tls_locally_signed_cert & {
		cert_request_pem:      csr.cert_request_pem
		ca_private_key_pem:    issuer_key
		ca_cert_pem:           issuer_cert
		validity_period_hours: 2160
		early_renewal_hours:   renewal_hours
		allowed_uses: ["server_auth"]
	}
	file: local.#Resource.local_file & {
		filename: "out/\(zone_name)/\(name).pem"
		content:  cert.cert_pem + issuer_cert
	}
}
```

```cue file=main.cue scene=base
package estate

import (
	loc "pravo.dev/providers/hashicorp/local"
	tlsp "pravo.dev/providers/hashicorp/tls"
	"example.com/estate/pki"
)

$tls:   tlsp.#Provider
$local: loc.#Provider

zones: [string]: services: [string]: renewal_hours: *0 | int

zones: {
	a: services: {api: {}, web: {}}
	b: services: {api: {}, web: {}}
	c: services: {api: {}, web: {}}
}

root: pki.#Root & {tls: $tls, local: $local}

zone: {
	for zn, _ in zones {
		(zn): pki.#Zone & {
			tls:         $tls
			local:       $local
			name:        zn
			issuer_key:  root.key.private_key_pem
			issuer_cert: root.cert.cert_pem
		}
	}
}

service: {
	for zn, z in zones {
		(zn): {
			for sn, svc in z.services {
				(sn): pki.#Service & {
					tls:           $tls
					local:         $local
					zone_name:     zn
					name:          sn
					renewal_hours: svc.renewal_hours
					issuer_key:    zone[zn].key.private_key_pem
					issuer_cert:   zone[zn].cert.cert_pem
				}
			}
		}
	}
}
```

## Apply the estate

```sh run scene=base
pravo init
pravo validate
pravo apply -auto-approve | tail -n 1
```

```text out
Configuration does not load yet; generating schemas for the declared providers first.
Providers:
  local = hashicorp/local v2.9.1
  tls = hashicorp/tls v4.4.1
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/local (2 resource types, 3 data sources, 1 ephemeral, 1 functions)
Up to date cue.mod/gen/pravo.dev/providers/hashicorp/tls (4 resource types, 2 data sources, 2 ephemeral, 0 functions)

pravo has been initialised. Run "pravo plan" to see what it would do.
Success! 39 resources, 0 data sources, 0 ephemeral, 0 function calls, 0 outputs across 2 providers.
Apply complete! Resources: 39 added, 0 changed, 0 destroyed. [txn <id>]
```

## The graph

`pravo state query` prints the type of every node. `pravo state graph` prints the edges that a plan follows: `a -> b` means that `a` reads `b`. `pravo state dependents` lists everything that a change to a node can reach.

```sh run scene=types
pravo state query '{for a, r in resources {(a): r.type}}'
```

```text out
{
	"root.cert":          "tls_self_signed_cert"
	"root.file":          "local_file"
	"root.key":           "tls_private_key"
	"service.a.api.cert": "tls_locally_signed_cert"
	"service.a.api.csr":  "tls_cert_request"
	"service.a.api.file": "local_file"
	"service.a.api.key":  "tls_private_key"
	"service.a.web.cert": "tls_locally_signed_cert"
	"service.a.web.csr":  "tls_cert_request"
	"service.a.web.file": "local_file"
	"service.a.web.key":  "tls_private_key"
	"service.b.api.cert": "tls_locally_signed_cert"
	"service.b.api.csr":  "tls_cert_request"
	"service.b.api.file": "local_file"
	"service.b.api.key":  "tls_private_key"
	"service.b.web.cert": "tls_locally_signed_cert"
	"service.b.web.csr":  "tls_cert_request"
	"service.b.web.file": "local_file"
	"service.b.web.key":  "tls_private_key"
	"service.c.api.cert": "tls_locally_signed_cert"
	"service.c.api.csr":  "tls_cert_request"
	"service.c.api.file": "local_file"
	"service.c.api.key":  "tls_private_key"
	"service.c.web.cert": "tls_locally_signed_cert"
	"service.c.web.csr":  "tls_cert_request"
	"service.c.web.file": "local_file"
	"service.c.web.key":  "tls_private_key"
	"zone.a.cert":        "tls_locally_signed_cert"
	"zone.a.csr":         "tls_cert_request"
	"zone.a.file":        "local_file"
	"zone.a.key":         "tls_private_key"
	"zone.b.cert":        "tls_locally_signed_cert"
	"zone.b.csr":         "tls_cert_request"
	"zone.b.file":        "local_file"
	"zone.b.key":         "tls_private_key"
	"zone.c.cert":        "tls_locally_signed_cert"
	"zone.c.csr":         "tls_cert_request"
	"zone.c.file":        "local_file"
	"zone.c.key":         "tls_private_key"
}
```

```sh run scene=edges
pravo state graph
```

```text out
root.cert -> root.key
root.file -> root.cert
service.a.api.cert -> service.a.api.csr
service.a.api.cert -> zone.a.cert
service.a.api.cert -> zone.a.key
service.a.api.csr -> service.a.api.key
service.a.api.file -> service.a.api.cert
service.a.api.file -> zone.a.cert
service.a.web.cert -> service.a.web.csr
service.a.web.cert -> zone.a.cert
service.a.web.cert -> zone.a.key
service.a.web.csr -> service.a.web.key
service.a.web.file -> service.a.web.cert
service.a.web.file -> zone.a.cert
service.b.api.cert -> service.b.api.csr
service.b.api.cert -> zone.b.cert
service.b.api.cert -> zone.b.key
service.b.api.csr -> service.b.api.key
service.b.api.file -> service.b.api.cert
service.b.api.file -> zone.b.cert
service.b.web.cert -> service.b.web.csr
service.b.web.cert -> zone.b.cert
service.b.web.cert -> zone.b.key
service.b.web.csr -> service.b.web.key
service.b.web.file -> service.b.web.cert
service.b.web.file -> zone.b.cert
service.c.api.cert -> service.c.api.csr
service.c.api.cert -> zone.c.cert
service.c.api.cert -> zone.c.key
service.c.api.csr -> service.c.api.key
service.c.api.file -> service.c.api.cert
service.c.api.file -> zone.c.cert
service.c.web.cert -> service.c.web.csr
service.c.web.cert -> zone.c.cert
service.c.web.cert -> zone.c.key
service.c.web.csr -> service.c.web.key
service.c.web.file -> service.c.web.cert
service.c.web.file -> zone.c.cert
zone.a.cert -> root.cert
zone.a.cert -> root.key
zone.a.cert -> zone.a.csr
zone.a.csr -> zone.a.key
zone.a.file -> zone.a.cert
zone.b.cert -> root.cert
zone.b.cert -> root.key
zone.b.cert -> zone.b.csr
zone.b.csr -> zone.b.key
zone.b.file -> zone.b.cert
zone.c.cert -> root.cert
zone.c.cert -> root.key
zone.c.cert -> zone.c.csr
zone.c.csr -> zone.c.key
zone.c.file -> zone.c.cert
```

```sh run scene=reach
for a in $(pravo state list); do echo "$a: $(pravo state dependents $a | tr '\n' ' ')"; done
```

```text out
root.cert: root.file service.a.api.cert service.a.api.file service.a.web.cert service.a.web.file service.b.api.cert service.b.api.file service.b.web.cert service.b.web.file service.c.api.cert service.c.api.file service.c.web.cert service.c.web.file zone.a.cert zone.a.file zone.b.cert zone.b.file zone.c.cert zone.c.file
root.file:
root.key: root.cert root.file service.a.api.cert service.a.api.file service.a.web.cert service.a.web.file service.b.api.cert service.b.api.file service.b.web.cert service.b.web.file service.c.api.cert service.c.api.file service.c.web.cert service.c.web.file zone.a.cert zone.a.file zone.b.cert zone.b.file zone.c.cert zone.c.file
service.a.api.cert: service.a.api.file
service.a.api.csr: service.a.api.cert service.a.api.file
service.a.api.file:
service.a.api.key: service.a.api.cert service.a.api.csr service.a.api.file
service.a.web.cert: service.a.web.file
service.a.web.csr: service.a.web.cert service.a.web.file
service.a.web.file:
service.a.web.key: service.a.web.cert service.a.web.csr service.a.web.file
service.b.api.cert: service.b.api.file
service.b.api.csr: service.b.api.cert service.b.api.file
service.b.api.file:
service.b.api.key: service.b.api.cert service.b.api.csr service.b.api.file
service.b.web.cert: service.b.web.file
service.b.web.csr: service.b.web.cert service.b.web.file
service.b.web.file:
service.b.web.key: service.b.web.cert service.b.web.csr service.b.web.file
service.c.api.cert: service.c.api.file
service.c.api.csr: service.c.api.cert service.c.api.file
service.c.api.file:
service.c.api.key: service.c.api.cert service.c.api.csr service.c.api.file
service.c.web.cert: service.c.web.file
service.c.web.csr: service.c.web.cert service.c.web.file
service.c.web.file:
service.c.web.key: service.c.web.cert service.c.web.csr service.c.web.file
zone.a.cert: service.a.api.cert service.a.api.file service.a.web.cert service.a.web.file zone.a.file
zone.a.csr: service.a.api.cert service.a.api.file service.a.web.cert service.a.web.file zone.a.cert zone.a.file
zone.a.file:
zone.a.key: service.a.api.cert service.a.api.file service.a.web.cert service.a.web.file zone.a.cert zone.a.csr zone.a.file
zone.b.cert: service.b.api.cert service.b.api.file service.b.web.cert service.b.web.file zone.b.file
zone.b.csr: service.b.api.cert service.b.api.file service.b.web.cert service.b.web.file zone.b.cert zone.b.file
zone.b.file:
zone.b.key: service.b.api.cert service.b.api.file service.b.web.cert service.b.web.file zone.b.cert zone.b.csr zone.b.file
zone.c.cert: service.c.api.cert service.c.api.file service.c.web.cert service.c.web.file zone.c.file
zone.c.csr: service.c.api.cert service.c.api.file service.c.web.cert service.c.web.file zone.c.cert zone.c.file
zone.c.file:
zone.c.key: service.c.api.cert service.c.api.file service.c.web.cert service.c.web.file zone.c.cert zone.c.csr zone.c.file
```

## Scene: replace an immutable attribute

The curve of the root key cannot change in place.

```cue file=pki/root.cue scene=replace
package pki

import (
	loc "pravo.dev/providers/hashicorp/local"
	tlsp "pravo.dev/providers/hashicorp/tls"
)

#Root: {
	tls:   tlsp.#Provider
	local: loc.#Provider

	key: tls.#Resource.tls_private_key & {
		algorithm:   "ECDSA"
		ecdsa_curve: "P384"
	}
	cert: tls.#Resource.tls_self_signed_cert & {
		private_key_pem: key.private_key_pem
		subject: [{common_name: "estate root"}]
		is_ca_certificate:     true
		validity_period_hours: 87600
		allowed_uses: ["cert_signing"]
	}
	file: local.#Resource.local_file & {
		filename: "out/root.pem"
		content:  cert.cert_pem
	}
}
```

```sh run scene=replace
pravo plan | grep -E '^(  #|Plan|Graph)'
```

```text out
  # root.key (tls_private_key) must be replaced
  # root.cert (tls_self_signed_cert) must be replaced
  # root.file (local_file) must be replaced
  # zone.a.cert (tls_locally_signed_cert) must be replaced
  # zone.a.file (local_file) must be replaced
  # zone.b.cert (tls_locally_signed_cert) must be replaced
  # zone.b.file (local_file) must be replaced
  # zone.c.cert (tls_locally_signed_cert) must be replaced
  # zone.c.file (local_file) must be replaced
  # service.a.api.cert (tls_locally_signed_cert) must be replaced
  # service.a.api.file (local_file) must be replaced
  # service.a.web.cert (tls_locally_signed_cert) must be replaced
  # service.a.web.file (local_file) must be replaced
  # service.b.api.cert (tls_locally_signed_cert) must be replaced
  # service.b.api.file (local_file) must be replaced
  # service.b.web.cert (tls_locally_signed_cert) must be replaced
  # service.b.web.file (local_file) must be replaced
  # service.c.api.cert (tls_locally_signed_cert) must be replaced
  # service.c.api.file (local_file) must be replaced
  # service.c.web.cert (tls_locally_signed_cert) must be replaced
  # service.c.web.file (local_file) must be replaced
Plan: 21 to add, 0 to change, 21 to destroy.
Graph: planned 21 of 39, 18 unchanged and skipped, 33 refreshed, 6 not read.
```

The curve goes back to P256 before the next scene.

```cue file=pki/root.cue scene=base
package pki

import (
	loc "pravo.dev/providers/hashicorp/local"
	tlsp "pravo.dev/providers/hashicorp/tls"
)

#Root: {
	tls:   tlsp.#Provider
	local: loc.#Provider

	key: tls.#Resource.tls_private_key & {
		algorithm:   "ECDSA"
		ecdsa_curve: "P256"
	}
	cert: tls.#Resource.tls_self_signed_cert & {
		private_key_pem: key.private_key_pem
		subject: [{common_name: "estate root"}]
		is_ca_certificate:     true
		validity_period_hours: 87600
		allowed_uses: ["cert_signing"]
	}
	file: local.#Resource.local_file & {
		filename: "out/root.pem"
		content:  cert.cert_pem
	}
}
```

## Scene: change a setting

One service asks for a longer renewal window. The provider updates the certificate in place.

```cue file=main.cue scene=setting
package estate

import (
	loc "pravo.dev/providers/hashicorp/local"
	tlsp "pravo.dev/providers/hashicorp/tls"
	"example.com/estate/pki"
)

$tls:   tlsp.#Provider
$local: loc.#Provider

zones: [string]: services: [string]: renewal_hours: *0 | int

zones: {
	a: services: {api: {}, web: {}}
	b: services: {api: {}, web: {renewal_hours: 48}}
	c: services: {api: {}, web: {}}
}

root: pki.#Root & {tls: $tls, local: $local}

zone: {
	for zn, _ in zones {
		(zn): pki.#Zone & {
			tls:         $tls
			local:       $local
			name:        zn
			issuer_key:  root.key.private_key_pem
			issuer_cert: root.cert.cert_pem
		}
	}
}

service: {
	for zn, z in zones {
		(zn): {
			for sn, svc in z.services {
				(sn): pki.#Service & {
					tls:           $tls
					local:         $local
					zone_name:     zn
					name:          sn
					renewal_hours: svc.renewal_hours
					issuer_key:    zone[zn].key.private_key_pem
					issuer_cert:   zone[zn].cert.cert_pem
				}
			}
		}
	}
}
```

```sh run scene=setting
pravo plan | grep -E '^(  #|Plan|Graph)'
```

```text out
  # service.b.web.cert (tls_locally_signed_cert) will be updated in-place
Plan: 0 to add, 1 to change, 0 to destroy.
Graph: planned 1 of 39, 38 unchanged and skipped, 4 refreshed, 35 not read.
```

The window goes back to its default before the next scene.

```cue file=main.cue scene=base
package estate

import (
	loc "pravo.dev/providers/hashicorp/local"
	tlsp "pravo.dev/providers/hashicorp/tls"
	"example.com/estate/pki"
)

$tls:   tlsp.#Provider
$local: loc.#Provider

zones: [string]: services: [string]: renewal_hours: *0 | int

zones: {
	a: services: {api: {}, web: {}}
	b: services: {api: {}, web: {}}
	c: services: {api: {}, web: {}}
}

root: pki.#Root & {tls: $tls, local: $local}

zone: {
	for zn, _ in zones {
		(zn): pki.#Zone & {
			tls:         $tls
			local:       $local
			name:        zn
			issuer_key:  root.key.private_key_pem
			issuer_cert: root.cert.cert_pem
		}
	}
}

service: {
	for zn, z in zones {
		(zn): {
			for sn, svc in z.services {
				(sn): pki.#Service & {
					tls:           $tls
					local:         $local
					zone_name:     zn
					name:          sn
					renewal_hours: svc.renewal_hours
					issuer_key:    zone[zn].key.private_key_pem
					issuer_cert:   zone[zn].cert.cert_pem
				}
			}
		}
	}
}
```

## Scene: two applies at once

Ana rotates the key of zone a. Bob rotates the key of zone b. Each saves a plan.

```sh run scene=lanes
pravo plan --replace zone.a.key --out a.plan --actor ana | grep -E '^(Plan|Graph)'
pravo plan --replace zone.b.key --out b.plan --actor bob | grep -E '^(Plan|Graph)'
```

```text out
Plan: 8 to add, 0 to change, 8 to destroy.
Graph: planned 8 of 39, 31 unchanged and skipped, 12 refreshed, 27 not read.
Plan: 8 to add, 0 to change, 8 to destroy.
Graph: planned 8 of 39, 31 unchanged and skipped, 12 refreshed, 27 not read.
```

The script waits until a resource is locked, which makes the run repeatable.

```sh file=wait-for-lock.sh title="wait-for-lock.sh" scene=lanes
for i in $(seq 1 200); do
	pravo state locks | grep -q "^$1 " && exit 0
	sleep 0.05
done
exit 1
```

Both applies start. When both keys are locked, the fifth command lists the locks. The last command shows that the locks are gone.

```sh run scene=locks
pravo apply a.plan --actor ana > a.log 2>&1 &
sh wait-for-lock.sh zone.a.key
pravo apply b.plan --actor bob > b.log 2>&1 &
sh wait-for-lock.sh zone.b.key
pravo state locks | cut -d, -f1
wait
grep -h '^Apply complete' a.log b.log
pravo state locks
```

```text out
service.a.api.cert  locked by ana
service.a.api.file  locked by ana
service.a.web.cert  locked by ana
service.a.web.file  locked by ana
service.b.api.cert  locked by bob
service.b.api.file  locked by bob
service.b.web.cert  locked by bob
service.b.web.file  locked by bob
zone.a.cert  locked by ana
zone.a.csr  locked by ana
zone.a.file  locked by ana
zone.a.key  locked by ana
zone.b.cert  locked by bob
zone.b.csr  locked by bob
zone.b.file  locked by bob
zone.b.key  locked by bob
Apply complete! Resources: 8 added, 0 changed, 8 destroyed. [txn <id>]
Apply complete! Resources: 8 added, 0 changed, 8 destroyed. [txn <id>]
No locks or reservations held.
```

## Scene: add a zone

A fourth zone has no services yet.

```cue file=main.cue scene=zone
package estate

import (
	loc "pravo.dev/providers/hashicorp/local"
	tlsp "pravo.dev/providers/hashicorp/tls"
	"example.com/estate/pki"
)

$tls:   tlsp.#Provider
$local: loc.#Provider

zones: [string]: services: [string]: renewal_hours: *0 | int

zones: {
	a: services: {api: {}, web: {}}
	b: services: {api: {}, web: {}}
	c: services: {api: {}, web: {}}
	d: services: {}
}

root: pki.#Root & {tls: $tls, local: $local}

zone: {
	for zn, _ in zones {
		(zn): pki.#Zone & {
			tls:         $tls
			local:       $local
			name:        zn
			issuer_key:  root.key.private_key_pem
			issuer_cert: root.cert.cert_pem
		}
	}
}

service: {
	for zn, z in zones {
		(zn): {
			for sn, svc in z.services {
				(sn): pki.#Service & {
					tls:           $tls
					local:         $local
					zone_name:     zn
					name:          sn
					renewal_hours: svc.renewal_hours
					issuer_key:    zone[zn].key.private_key_pem
					issuer_cert:   zone[zn].cert.cert_pem
				}
			}
		}
	}
}
```

```sh run scene=zone
pravo plan | grep -E '^(  #|Plan|Graph)'
```

```text out
  # zone.d.key (tls_private_key) will be created
  # zone.d.csr (tls_cert_request) will be created
  # zone.d.cert (tls_locally_signed_cert) will be created
  # zone.d.file (local_file) will be created
Plan: 4 to add, 0 to change, 0 to destroy.
Graph: planned 4 of 43, 39 unchanged and skipped, 2 refreshed, 37 not read.
```

The apply adds the four resources. The state records their edges and what depends on them.

```sh run scene=zone-edges
pravo apply -auto-approve | tail -n 1
pravo state graph | grep '^zone.d'
for a in root.key root.cert $(pravo state list | grep '^zone.d'); do echo "$a: $(pravo state dependents $a | tr '\n' ' ')"; done
```

```text out
Apply complete! Resources: 4 added, 0 changed, 0 destroyed. [txn <id>]
zone.d.cert -> root.cert
zone.d.cert -> root.key
zone.d.cert -> zone.d.csr
zone.d.csr -> zone.d.key
zone.d.file -> zone.d.cert
root.key: root.cert root.file service.a.api.cert service.a.api.file service.a.web.cert service.a.web.file service.b.api.cert service.b.api.file service.b.web.cert service.b.web.file service.c.api.cert service.c.api.file service.c.web.cert service.c.web.file zone.a.cert zone.a.file zone.b.cert zone.b.file zone.c.cert zone.c.file zone.d.cert zone.d.file
root.cert: root.file service.a.api.cert service.a.api.file service.a.web.cert service.a.web.file service.b.api.cert service.b.api.file service.b.web.cert service.b.web.file service.c.api.cert service.c.api.file service.c.web.cert service.c.web.file zone.a.cert zone.a.file zone.b.cert zone.b.file zone.c.cert zone.c.file zone.d.cert zone.d.file
zone.d.cert: zone.d.file
zone.d.csr: zone.d.cert zone.d.file
zone.d.file:
zone.d.key: zone.d.cert zone.d.csr zone.d.file
```
