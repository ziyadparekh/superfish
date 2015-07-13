# superfish
The API interface for SuperFish

This is the Backend to run the [Superfish](https://github.com/ziyadparekh/superfish-client) app. It is written in Go and all in one file `chat.go` (I know bad idea but its just over 1000 lines so kind of maintainable). This app was a sideproject to help me learn Go and Objective C. But I learnt a tremendous amount developing it. 

Its easy enough to run. Try `Godep get` and then `go run chat.go` to run the server. It listens on `:8080` and can be accessed through a REST platform like Postman. It uses HTTP for most routes:
1. Registration
2. Login
3. Group Creation
4. Group Retrieval
5. Group Editing
6. Contacts API
7. Messages Retrieval

Realtime communication is done through sockets. 

1. `/signup` create a new account (POST)
2. `/login` login to your account (POST)
3. `/contacts` Filter contacts from iOS addressbook (POST)
4. `/contacts` Fetch filtered contacts (GET)
5. `/group` Create new group (POST)
6. `/group/{group_id}` Fetch group details (GET)
7. `/group/{group_id}/messages` Get paginated messages for group (GET)
8. `/group/{group_id}/name` update group name (PUT)
9. `/group/{group_id}/members` update group members (PUT)
10. `/groups` Fetch users groups (GET)
11. `/ws/chatbot/{user_id}` register Hubot to system (socket)
12. `/ws/{group_id}` register user to group (socket)

This was built on the work of many open source projects and thus is also open sourced. I hope it provides some knowledge to those working on realtime apps.

I will update the README in the comming days. This was a very spontaneous decision to open source it. 

If you have any questions or concerns please file issues or reach me on Twitter [@ziyadparekh]( https://twitter.com/ziyadparekh)

Thanks and hope you find it useful to play around with. 

Pull requests are welcome and if you think this is something worth developing furthur I would love to increase the pace of development.
