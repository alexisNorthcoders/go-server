# go-server

Accounts, scores and ratings for the games. The multiplayer server reports match results here.

## Language

**Account**: a registered user (a row in `users`). Only Accounts have a Rating.
**Guest**: someone playing with an anonymous token. Their token carries the username `"anonymous"`, but the reliable test is that the `userId` is not a registered user.
**Ranked match**: a multiplayer match whose result changes Ratings.
**Rating**: an Account's Glicko-2 skill estimate, 1500 by default. Each Ranked match is its own rating period.
**Provisional**: a Rating based on fewer than 5 Ranked matches.
**Stand-in**: a bot that plays a Ranked match in place of a human. It has a fixed Rating that is never stored or changed; it only serves as the opponent's rating in the calculation.
**Forfeit**: a Ranked match ended by one side giving up. It counts as a loss for that side.
