# syntax=docker.io/docker/dockerfile:1.3@sha256:42399d4635eddd7a9b8a24be879d2f9a930d0ed040a61324cfdf59ef1357b3b2

FROM node:lts-alpine3.15@sha256:a2c7f8ebdec79619fba306cec38150db44a45b48380d09603d3602139c5a5f92

RUN mkdir -p /app
WORKDIR /app

RUN apk add python3 \
        make \
        g++ 

ADD . .

RUN echo $(ls -1 .)
RUN echo $(less Dockerfile)

WORKDIR ./relayer/spy_relayer

RUN npm ci --force && \
    npm run build

#TODO don't hardcode for tilt but accept env file
# RUN --mount=type=cache,uid=1000,gid=1000,target=/home/node/.npm \
#   npm run tilt_relay

RUN npx hardhat run lib/main.js --network fuji





