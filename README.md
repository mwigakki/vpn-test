### 这是一个自制 vpn 的简单实现

server 使用云厂商的云主机，client 使用个人 windows10 电脑，客户端程序涉及改网络配置，需要管理员程序运行。并确保 [wintun.dll](https://www.wintun.net/) 文件和 client.exe 在相同目录。它是windows的虚拟网卡接口驱动。

为机器创建 P2P 虚拟网卡使用 github.com/songgao/water 和 github.com/labulakalia/water （windows 客户端）

代码很简单，主要是需要给 server 和 client 做网络和路由修改比较麻烦

#### server
``` bash
# 假设 server tun IP = 10.8.0.1/24 , client tun IP = 10.8.0.2/24
# 为 tun0（你的 TUN 设备）设置 IP 地址。
sudo ip addr add 10.8.0.1/24 dev tun0 
# 启用 tun0，使接口从 DOWN 变成 UP。
sudo ip link set dev tun0 up

# 允许内核转发（临时）
sudo sysctl -w net.ipv4.ip_forward=1

# NAT 使得从 VPN 来的流量可以出去到公网（若需要访问互联网），让从 TUN（10.8.0.x）来的包伪装成 server 真实公网 IP，再发往外网。
sudo iptables -t nat -A POSTROUTING -s 10.8.0.0/24 -o eth0 -j MASQUERADE
# 但是很多云厂商的Linux 服务器缺少 FORWARD 链放通，所以此时还需要允许 TUN 流量进入转发链
sudo iptables -A FORWARD -i tun0 -o eth0 -j ACCEPT
sudo iptables -A FORWARD -i eth0 -o tun0 -m state --state ESTABLISHED,RELATED -j ACCEPT
# 使用sudo iptables -L FORWARD -n -v 检查
```

#### client
``` bash
# 1.给 TUN 网卡配置 IP，网卡名称 WaterIface 在执行client程序会得到，地址必须和 server 在同一网段（10.8.0.x）。
netsh interface ip set address name="WaterIface" static 10.8.0.2 255.255.255.0
## 删除命令如下：netsh interface ip delete address name="WaterIface" addr=10.8.0.2

# 2.告诉系统“VPN 服务器的虚拟 IP 走 TUN 网卡”，（这条命令执行可能会提示对象已存在，是因为执行上面的命令会自动添加直连路由（在链路上））
route add 10.8.0.1 mask 255.255.255.255 10.8.0.2 if <WaterIface_Idx>
# 其中 WaterIface_Idx 用下面命令查看：route print 或 netsh interface ipv4 show interfaces
## 删除该路由的命令：route delete 10.8.0.1

# 3. 选择性地添加路由
# 3.1 只把 server 推送的网段走 VPN（推荐）
route add 10.8.0.0 mask 255.255.255.0 10.8.0.1
## 删除该路由的命令：route delete 10.8.0.0

# 3.2 全局代理（所有流量走 VPN）
## 首先增加全局默认路由走vpn对端地址，然后增加到 server 的路由（必须有这条路由，不然所有流量到出不去了）
route add 0.0.0.0 mask 0.0.0.0 10.8.0.1 if <WaterIface_Idx>  
route add <server公网地址> mask 255.255.255.255 <真实网卡的网关> if <你的真实网卡 ifIndex>
```