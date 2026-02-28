# Advanced DaemonSet 节点标签补丁功能使用指南

## 概述
Advanced DaemonSet 现在支持基于节点标签的差异化补丁配置，允许您使用单个 DaemonSet 管理异构节点集群中的不同配置。

## 快速开始

### 1. 基本配置示例

```yaml
apiVersion: apps.kruise.io/v1beta1
kind: DaemonSet
metadata:
  name: my-daemonset
spec:
  selector:
    matchLabels:
      app: myapp
  template:
    metadata:
      labels:
        app: myapp
    spec:
      containers:
      - name: main
        image: myimage:latest
        env:
        - name: DEFAULT_VALUE
          value: "standard"
  patches:
  # 大内存节点配置
  - selector:
      matchLabels:
        node-type: high-memory
    priority: 100
    patch:
      spec:
        containers:
        - name: main
          env:
          - name: CACHE_SIZE
            value: "8Gi"
          resources:
            requests:
              memory: "4Gi"
  
  # GPU节点配置
  - selector:
      matchLabels:
        accelerator: nvidia-tesla
    priority: 90
    patch:
      spec:
        containers:
        - name: main
          env:
          - name: GPU_ENABLED
            value: "true"
          resources:
            requests:
              nvidia.com/gpu: 1
```

### 2. 节点标签设置

在节点上设置标签以便 DaemonSet 识别：

```bash
# 标记大内存节点
kubectl label nodes node1 node-type=high-memory

# 标记GPU节点
kubectl label nodes node2 accelerator=nvidia-tesla

# 标记标准节点
kubectl label nodes node3 node-type=standard
```

## 配置说明

### patches 字段结构

```yaml
patches:
- selector:          # 节点选择器（必需）
    matchLabels:
      key: value
  priority: 50       # 优先级（可选，默认0）
  patch:             # 补丁内容（必需）
    spec:
      containers:
      - name: container-name
        ...
```

### 优先级机制
- 优先级数值越高，优先级越高
- 当多个补丁匹配同一节点时，按优先级降序应用
- 相同优先级的补丁，按定义顺序应用

### 补丁格式
- 使用 Kubernetes Strategic Merge Patch 格式
- 仅支持对 PodTemplateSpec 的补丁
- 必须是有效的 JSON 格式

## 验证规则

1. **最大补丁数**：每个 DaemonSet 最多 10 个补丁
2. **补丁大小**：每个补丁数据不超过 1KB
3. **选择器验证**：必须是有效的标签选择器
4. **JSON格式**：补丁必须是有效的 JSON 和 Strategic Merge Patch

## 使用场景

### 场景1：存储差异化配置
```yaml
patches:
- selector:
    matchLabels:
      disk-type: ssd
  patch:
    spec:
      containers:
      - name: storage-agent
        env:
        - name: CACHE_PATH
          value: "/fast-cache"
        - name: CACHE_SIZE
          value: "10Gi"
```

### 场景2：CPU架构差异化
```yaml
patches:
- selector:
    matchLabels:
      kubernetes.io/arch: arm64
  patch:
    spec:
      containers:
      - name: compute-agent
        image: myimage:latest-arm64
```

### 场景3：网络环境差异化
```yaml
patches:
- selector:
    matchLabels:
      network-zone: edge
  patch:
    spec:
      containers:
      - name: network-agent
        env:
        - name: BANDWIDTH_LIMIT
          value: "100Mbps"
        - name: COMPRESSION
          value: "aggressive"
```

## 故障排查

### 检查补丁应用状态
```bash
# 查看DaemonSet详情
kubectl describe daemonset <name>

# 检查Pod配置差异
kubectl get pods -l app=myapp -o yaml | grep -A 10 "env:"

# 验证节点标签
kubectl get nodes --show-labels
```

### 常见问题

1. **补丁未生效**：检查节点标签是否正确匹配选择器
2. **验证错误**：检查补丁格式是否正确
3. **优先级冲突**：检查优先级设置

## 升级兼容性

- 向后兼容：不设置 patches 字段时保持原有行为
- 平滑升级：现有 DaemonSet 无需修改
- 回滚支持：删除 patches 字段即可回滚

## 限制说明

- 不支持基于节点注解或污点的选择
- 不支持动态配置更新（需要重建Pod）
- 补丁仅应用于新创建的Pod，不影响已存在的Pod

