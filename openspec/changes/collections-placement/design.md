## Context

The Pulumi adapter and the TFE adapter must reach the same placement outcome. Rules reference collections by stable ID, so a rename of a collection keeps the rule. The namespace create writes the default collection and the catch-all rule in its own transaction.

## Decisions

1. **One resolver for CLI placement.** An unexported placementResolver is made only by NewPlacementResolver from the ordered rules and the collections read in the create transaction. Adapters pass tool, project and unit name and get a collection ID or a typed refusal.
2. **Default at namespace create.** The namespace create writes the default collection, the catch-all rule and their audit entries in one transaction with the namespace. A request flag skips the two. The catch-all rule is stored last and refers to the default collection by stable ID.
3. **Validate at save.** A rule save checks the pattern and that the target collection ID exists. A pattern is a glob of at most 128 bytes, printable ASCII, no regular expressions and at most 4 wildcard characters. A namespace holds at most 100 rules. An empty list gives the no-match refusal.
4. **Audit in the same transaction.** Each placement and each rule save, reorder or delete writes its audit entry in the same transaction as the unit insert or the rule change. If the audit write fails, the transaction rolls back and no unit exists. A refusal writes its entry in its own transaction.
5. **Same-name creates.** The unit insert has a unique name constraint in the transaction, shared by the Pulumi and TFE paths. Of two concurrent creates of one name, one stores the unit and the other gets a conflict error and an audit entry.
6. **Rule list order.** The rule list API returns rules in match order with the collection ID of each rule, to a caller who can list rules, whether or not the caller can read the collection.

## Structure

Unexported type placementResolver with constructor NewPlacementResolver(rules, collections). Rules hold the collection ID, never the name. Adapters hold no pattern logic.

## Risks

- A broad early rule shadows a later narrow rule. The rule list API shows the order.
- A TFE project that matches no collection blocks workspace creates in that project. The refusal is audited and names the fix.
- A rule can point to a collection that was deleted later. The create is refused with the missing-collection error.
