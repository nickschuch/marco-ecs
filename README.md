# Marco - ECS

ECS support for https://github.com/nickschuch/marco

## Usage

```bash
$ marco-ecs --marco=http://localhost:81 \
            --cluster="prod"
```

NOTE: We assume the Marco daemon is already running.

## Docker

The following will setup Marco + ECS backend pushes.

```bash
$ docker run -d \
             --name=marco \
             -p 0.0.0.0:80:80 nickschuch/marco
$ docker run -d \
             --link marco:marco \
             -e "MARCO_ECS_URL=http://marco:81" \
             -e "MARCO_ECS_REGION=us-east-1" \
             -e "MARCO_ECS_CLUSTER=default" nickschuch/marco-ecs
```
