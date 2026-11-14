# 二进制由 Makefile 先在本地交叉编译好再拷进镜像, 与老项目保持一致
FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY go-onebots /app/go-onebots

# PocketBase 的数据目录: 数据库 / 上传文件 / S3 配置都在这里
VOLUME /app/pb_data

EXPOSE 8090

ENTRYPOINT ["/app/go-onebots"]
CMD ["serve", "--http=0.0.0.0:8090"]
