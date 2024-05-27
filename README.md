# 日志插件
提供如下功能：
1. Window Analytical日志读取
2. Window Audit日志读取
3. DNS抓包读取

## 文件结构
1. build： 存放编译脚本
2. cmd：
   1. dnslogtail： linux下使用的dns抓包插件
   2. wineventtail： window下Analytical/Audit日志及dns抓包插件
3. consumer： 消费实现，目前实现了syslog、ftp/sftp
4. reader：解析实现，目前实现了elt文件、evtx文件读取及dns抓包
5. eventlog：定义日志结构体

## 编译
由于gopacket库的原因，没办法像之前一样在本机直接交叉编译，window目前是支持的，故参考[golang-crossbuild](https://github.com/elastic/golang-crossbuild)提供的交叉编译的方案实现。

1. 首先保证本机安装了docker
2. 然后根据不同架构进行执行
    ```shell
    cd build && sh build.sh linux amd64
    cd build && sh build.sh windows amd64
    cd build && sh build.sh linux arm64
    ```
编译好的文件会存放在deploy目录下:
* linux
   * amd64
   * arm64
* windows
   * amd64
# 打包
```shell
cd build && sh pack.sh linux amd64
cd build && sh pack.sh windows amd64
cd build && sh pack.sh linux arm64
```
打包文件会存放在deploy目录下:
* linux
  * amd64
  * arm64
* windows
  * amd64