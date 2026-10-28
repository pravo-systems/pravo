package platform

import "pravo.dev/pravo"

// The policies are judged at every apply. A plan that destroys or replaces
// anything waits for an admin; rendered files stay below out/; the estate is
// never destroyed as a whole.
approve_deletes: pravo.#Policy & {
	description: "A plan that destroys or replaces a resource is approved by an admin first"
	rule: {
		{summary: destroy: 0} | {summary: destroy: >0, approvals: {min: 1, role: "admin"}}
	}
}

files_under_out: pravo.#Policy & {
	description: "Rendered files are written below out/"
	rule: changes: [...{
		type:   string
		action: string
		after?: filename?: string
		if (type == "local_file" || type == "local_sensitive_file") && action != "delete" {
			after: filename: =~"^out/"
		}
	}]
}

no_destroy_runs: pravo.#Policy & {
	description: "The estate is not destroyed as a whole"
	rule: destroy?: false
}
