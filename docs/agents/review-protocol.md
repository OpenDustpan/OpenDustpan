# Review protocol

A reviewer is a new session. It reviews one pull request. The PM gives it the
pull request number and the session ID of the author. The reviewer and the
author send the must-fix items and the fixes to each other directly. The PM
gets a message only for CLEAN, for the end of round 3, or for a blocker.

## Before round 1

1. Read `CLAUDE.md`, `GLOSSARY.md` and the pull request body.
2. Find the `make checks-agent` summary in the pull request body. It must show
   exit 0 and name the current head sha of the pull request. If it does not,
   send the author one message that asks for it, and wait. Do not review.
3. Find the mutation self-check in the pull request body. It lists each
   defense layer of the change and the test that fails without that layer. If
   the change has security code and the list is not there, ask the author for
   it, and wait.

## Rounds

| Round | Scope | Model |
| --- | --- | --- |
| 1 | A full review of the diff. | The model of the brief: Opus for security code, design and RFCs, Sonnet for other changes. |
| 2 | A re-check of the must-fix items of round 1 only. | Sonnet. A re-check of a security item uses Opus. |
| 3 | A re-check of the must-fix items of round 2 only. | Sonnet. A re-check of a security item uses Opus. |

There is no round 4.

## A round

1. Review the current head. Write the head sha in the first line of the
   verdict.
2. Do the mutation check for each defense layer that the author listed:
   remove the layer, run the named test, and see it fail. Put the layer back.
   Check that the file is equal to the original.
3. Post the verdict on the pull request as one comment. The first line is
   `Review <round> at <sha>: CLEAN` or `Review <round> at <sha>: CHANGES`.
   Give each must-fix item a number, a file and line, and the reason. Put
   optional items in a separate list.
4. For CHANGES: send the author the must-fix list with `send_message`. Do
   not send it to the PM.
5. For CLEAN: send the PM one message:
   `PR <number> CLEAN at <sha>, round <round>`.
6. When round 3 ends with CHANGES, send the PM one message with the open
   must-fix items. Do not start a round 4.

## The author

1. Fix each must-fix item. Do not change other code.
2. For each item, write in one comment on the pull request: the item number,
   the fix commit, and the test that covers it.
3. Commit, run `make checks-agent` on the new head, and paste its summary in
   the comment.
4. Send the reviewer one message: `PR <number> fixed at <sha>`.
5. After CLEAN, do not change the branch.

## Blockers

The reviewer or the author sends the PM a message only for these blockers:

- the author does not answer.
- a must-fix item needs a decision of the PM or of the owner.
- a tool or a classifier refuses a command.

Never contact the project owner.
