package team

import "pravo.dev/pravo"

second_opinion: pravo.#Policy & {
	description: "A plan that destroys a resource is applied once another writer has approved it."
	rule: {
		{summary: destroy: 0} | {summary: destroy: >0, approvals: {min: 1}}
	}
}
