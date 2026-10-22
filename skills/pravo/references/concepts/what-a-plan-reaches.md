<!-- Generated from docs/concepts/what-a-plan-reaches.md by tools/skillgen. Do not edit. -->

# What a plan reaches

Change one setting, replace a root key, add a zone or run two applies on one estate, and see which resources each plan reaches and which locks each apply holds.

pravo records which resource reads which. A plan starts at the change and follows the edges for as long as a value changes. The resources it does not reach are neither validated nor planned, and their records are not read from the providers: a plan reads the records of the resources it reaches and of the resources they read, and only when a record is older than its refresh age. The `Graph:` line counts what was planned, skipped, read and not read.

An apply locks the resources that its plan writes, and nothing else. Two applies that write different resources run at the same time. The estate below is a small certificate authority with several zones, planned by the real binary.

[Plans](plans.md#scoped-plans) explains the rule with the commands that print each result. [State](state.md#locks-and-reservations) describes the locks, and [Lab 2](https://pravo.systems/learn/lab2-two-applies-at-once.html) runs two applies at once.
