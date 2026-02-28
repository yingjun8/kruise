# KEP-20260228: Advanced DaemonSet 基于节点标签的补丁功能

## 摘要

本提案为 OpenKruise Advanced DaemonSet 引入一项新功能：允许用户根据节点标签对 Pod 模板应用差异化补丁。该功能解决了在异构节点集群中使用单个 DaemonSet 管理不同配置的挑战。

## 动机

### 问题陈述

当前 Advanced DaemonSet 对所有节点使用统一的 Pod 模板，这在异构集群中带来挑战，不同节点类型需要不同配置。常见场景包括：

- **大容量存储节点** vs **标准节点** 需要不同的缓存配置
- **GPU节点** vs **CPU节点** 需要不同的资源请求
- **边缘节点** vs **云端节点** 需要不同的环境变量
- **大内存节点** vs **标准节点** 需要不同的JVM堆大小

### 实际使用案例

#### 案例1：Nydus镜像加速配置
作为集群管理员，我需要在所有节点上部署Nydus守护进程，并根据节点存储容量自动配置缓存大小。

```yaml
apiVersion: apps.kruise.io/v1beta1
kind: DaemonSet
metadata:
  name: nydus-daemonset
spec:
  selector:
    matchLabels:
      app: nydus
  template:
    metadata:
      labels:
        app: nydus
    spec:
      containers:
      - name: nydusd
        image: nydus:latest
        env:
        - name: LOCAL_CACHE_SIZE
          value: "500Gi"  # 标准节点默认值
  patches:
  - selector:
      matchLabels:
        node-role/nydus-storage: large
    patch:
      spec:
        containers:
        - name: nydusd
          env:
          - name: LOCAL_CACHE_SIZE
            value: "2Ti"
          - name: FEATURE_GATE_LARGE_DISK
            value: "true"
```

#### 案例2：GPU节点专项配置
作为ML平台工程师，我需要在GPU和CPU节点上部署不同配置的监控代理。

```yaml
apiVersion: apps.kruise.io/v1beta1
kind: DaemonSet
metadata:
  name: gpu-monitor
spec:
  patches:
  - selector:
      matchLabels:
        accelerator: nvidia-tesla-v100
    patch:
      spec:
        containers:
        - name: monitor
          resources:
            requests:
              nvidia.com/gpu: 1
              memory: 2Gi
          env:
          - name: GPU_MONITORING
            value: "enabled"
```

### 现有解决方案及局限性

| 方案 | 问题 |
|------|------|
| 多个DaemonSet | 配置重复、升级复杂、维护开销大 |
| Mutating Webhook | 额外服务部署、证书管理、性能开销 |
| 节点级ConfigMap | 需要Pod重启配置变更、灵活性有限 |

## 提案

### 目标

- 支持单个Advanced DaemonSet管理异构节点上的不同配置
- 保持与现有DaemonSet配置的向后兼容性
- 提供直观且符合Kubernetes习惯的配置体验
- 支持简单和复杂的补丁场景

### 非目标

- 支持基于节点注解或污点的非标签节点选择
- 无需Pod重建的动态配置更新
- 跨命名空间配置共享

### 用户故事

详见上文实际使用案例部分。

## 设计细节

### API规范

#### 新增API类型

```go
// DaemonSetSpec 扩展
type DaemonSetSpec struct {
    // ... 现有字段 ...
    
    // Patches 定义了基于节点标签匹配要应用到Pod模板的补丁列表
    // +optional
    Patches []DaemonSetPatch `json:"patches,omitempty"`
}

// DaemonSetPatch 定义了当节点标签匹配选择器时要应用的补丁
type DaemonSetPatch struct {
    // Selector 是节点标签查询，应该匹配此补丁
    Selector *metav1.LabelSelector `json:"selector"`
    
    // Patch 包含要应用到Pod模板的补丁
    // 补丁遵循Kubernetes策略合并补丁格式
    Patch runtime.RawExtension `json:"patch"`
    
    // Priority 定义了多个补丁匹配时的应用顺序
    // 较高值具有较高优先级
    // +optional
    Priority int32 `json:"priority,omitempty"`
}
```

#### 验证规则

1. **补丁大小限制**: 每个补丁不得超过1KB大小
2. **总补丁限制**: 每个DaemonSet最多10个补丁
3. **选择器验证**: 必须是有效的标签选择器格式
4. **补丁验证**: 必须是PodTemplateSpec的有效策略合并补丁
5. **冲突解决**: 列表中后面的补丁在选择器重叠时覆盖前面的

### 控制器实现

#### Pod创建流程增强

```go
func (dsc *ReconcileDaemonSet) createPodWithPatches(
    ctx context.Context,
    ds *appsv1beta1.DaemonSet,
    nodeName string,
    baseTemplate *corev1.PodTemplateSpec,
) (*corev1.Pod, error) {
    
    // 获取节点信息
    node, err := dsc.nodeLister.Get(nodeName)
    if err != nil {
        return nil, fmt.Errorf("获取节点 %s 失败: %v", nodeName, err)
    }
    
    // 从基础模板开始
    patchedTemplate := baseTemplate.DeepCopy()
    
    // 按优先级顺序应用匹配补丁
    patches := sortPatchesByPriority(ds.Spec.Patches)
    
    for _, patch := range patches {
        if matchesNodeSelector(node, patch.Selector) {
            patchedTemplate, err = applyStrategicMergePatch(
                patchedTemplate, 
                patch.Patch.Raw,
            )
            if err != nil {
                return nil, fmt.Errorf("应用补丁失败: %v", err)
            }
        }
    }
    
    return &corev1.Pod{
        ObjectMeta: patchedTemplate.ObjectMeta,
        Spec:       patchedTemplate.Spec,
    }, nil
}
```

#### 补丁应用逻辑

```go
func applyStrategicMergePatch(
    template *corev1.PodTemplateSpec,
    patchData []byte,
) (*corev1.PodTemplateSpec, error) {
    
    // 将模板转换为JSON
    originalJSON, err := json.Marshal(template)
    if err != nil {
        return nil, err
    }
    
    // 应用策略合并补丁
    patchedJSON, err := strategicpatch.StrategicMergePatch(
        originalJSON, 
        patchData, 
        &corev1.PodTemplateSpec{},
    )
    if err != nil {
        return nil, err
    }
    
    // 转换回PodTemplateSpec
    var patchedTemplate corev1.PodTemplateSpec
    if err := json.Unmarshal(patchedJSON, &patchedTemplate); err != nil {
        return nil, err
    }
    
    return &patchedTemplate, nil
}
```

#### 节点标签匹配

```go
func matchesNodeSelector(node *corev1.Node, selector *metav1.LabelSelector) bool {
    if selector == nil {
        return false
    }
    
    labelSelector, err := metav1.LabelSelectorAsSelector(selector)
    if err != nil {
        return false
    }
    
    return labelSelector.Matches(labels.Set(node.Labels))
}
```

### 版本管理

#### 哈希计算增强

为确保正确的版本管理，控制器版本哈希必须包含补丁信息：

```go
func computeHashWithPatches(
    ds *appsv1beta1.DaemonSet,
    nodeLabels map[string]string,
) string {
    
    // 包含基础模板哈希
    baseHash := kubecontroller.ComputeHash(&ds.Spec.Template, nil)
    
    // 包含适用补丁
    var patchHashes []string
    patches := getApplicablePatches(ds.Spec.Patches, nodeLabels)
    
    for _, patch := range patches {
        patchHash := sha256.Sum256(patch.Patch.Raw)
        patchHashes = append(patchHashes, hex.EncodeToString(patchHash[:8]))
    }
    
    // 组合哈希
    combined := fmt.Sprintf("%s-%s", baseHash, strings.Join(patchHashes, ""))
    return kubecontroller.ComputeHash(&corev1.PodTemplateSpec{
        ObjectMeta: metav1.ObjectMeta{
            Annotations: map[string]string{
                "daemonset.kruise.io/patch-combined-hash": combined,
            },
        },
    }, nil)
}
```

### 推出策略集成

#### 带补丁的原位更新

当补丁更新时，控制器应该：

1. **识别受影响Pod**: 找到节点标签匹配变更补丁的Pod
2. **计算新哈希**: 生成包含补丁的新控制器版本哈希
3. **尊重更新策略**: 遵守partition、maxSurge、maxUnavailable设置
4. **处理冲突**: 确保补丁变更不会与原位更新冲突

### 安全考虑

#### RBAC要求

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kruise-daemonset-patches-controller
rules:
- apiGroups: [""]
  resources: ["nodes"]
  verbs: ["get", "list", "watch"]
- apiGroups: ["apps.kruise.io"]
  resources: ["daemonsets"]
  verbs: ["get", "list", "watch", "update", "patch"]
```

#### 准入Webhook验证

```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingWebhookConfiguration
metadata:
  name: daemonset-patches-validator
webhooks:
- name: daemonset-patches.kruise.io
  rules:
  - apiGroups: ["apps.kruise.io"]
    apiVersions: ["v1beta1"]
    operations: ["CREATE", "UPDATE"]
    resources: ["daemonsets"]
  clientConfig:
    service:
      name: kruise-webhook-service
      namespace: kruise-system
      path: "/validate-daemonset-patches"
```

### 测试计划

#### 单元测试

1. **API验证测试**
   - 有效补丁配置
   - 无效补丁格式
   - 选择器验证

2. **控制器逻辑测试**
   - 补丁应用正确性
   - 节点标签匹配
   - 带优先级的多个补丁
   - 版本哈希计算

3. **边界情况测试**
   - 空补丁列表
   - 不匹配的选择器
   - 无效补丁内容
   - 大量补丁

#### 集成测试

1. **端到端场景**
   - 部署带补丁的DaemonSet
   - 验证不同节点上的补丁应用
   - 测试补丁变更的滚动更新
   - 测试回滚功能

2. **性能测试**
   - 1000+节点集群带补丁
   - 大补丁的内存使用
   - 补丁匹配的CPU影响

#### E2E测试

```go
var _ = SIGDescribe("AdvancedDaemonSet节点标签补丁", func() {
    
    ginkgo.It("应该基于节点标签应用补丁", func() {
        ds := &appsv1beta1.DaemonSet{
            Spec: appsv1beta1.DaemonSetSpec{
                Patches: []appsv1beta1.DaemonSetPatch{
                    {
                        Selector: &metav1.LabelSelector{
                            MatchLabels: map[string]string{"disk-type": "large"},
                        },
                        Patch: runtime.RawExtension{
                            Raw: []byte(`{"spec":{"containers":[{"name":"test","env":[{"name":"CACHE_SIZE","value":"2Ti"}]}]}}`),
                        },
                    },
                },
            },
        }
        
        // 创建DaemonSet并验证补丁应用
        framework.ExpectNoError(createDaemonSetWithPatches(f, ds))
        framework.ExpectNoError(verifyPatchApplication(f, ds))
    })
})
```

### 毕业标准

#### Alpha (v1.7.0)

- [ ] 功能在特性门控后实现 `AdvancedDaemonSetNodeLabelPatches`
- [ ] 基础补丁功能工作
- [ ] 单元测试覆盖率>80%
- [ ] 包含示例的文档

#### Beta (v1.8.0)

- [ ] 默认启用特性门控
- [ ] E2E测试通过
- [ ] 性能基准文档化
- [ ] 用户反馈纳入

#### GA (v1.9.0)

- [ ] 移除特性门控
- [ ] 生产使用3+个月
- [ ] 2+个月无关键bug报告
- [ ] 完整文档和示例

### 生产就绪审查

#### 特性启用和回滚

- **启用**: 由特性门控 `AdvancedDaemonSetNodeLabelPatches` 控制
- **回滚**: 禁用特性门控，现有Pod保持不变
- **升级**: 自动迁移，无需用户操作

#### 可扩展性

- **每个DaemonSet最大补丁数**: 10
- **最大补丁大小**: 每个补丁1KB
- **性能影响**: Pod创建额外延迟<1ms

#### 监控

- **指标**:
  - `daemonset_patches_applied_total`: 补丁应用计数器
  - `daemonset_patches_errors_total`: 补丁失败计数器
  - `daemonset_patches_duration_seconds`: 补丁应用时间直方图

### 实施历史

| 日期 | PR | 描述 |
|------|----|------|
| 2026-02-28 | TBD | 初始KEP创建 |
| TBD | TBD | Alpha实现 |
| TBD | TBD | Beta增强 |
| TBD | TBD | GA毕业 |

### 缺点

1. **复杂度增加**: 更复杂的API和控制器逻辑
2. **调试困难**: 调试补丁相关问题更难
3. **资源使用**: 补丁存储的内存使用略有增加

### 替代方案

#### 替代方案1: WorkloadSpread集成
- **优点**: 重用现有基础设施
- **缺点**: 需要额外CRD，用户体验更复杂

#### 替代方案2: Mutating Webhook
- **优点**: 无需API变更
- **缺点**: 外部依赖、性能开销、运维复杂度

### 基础设施需求

1. **CI/CD变更**: 为补丁添加新测试套件
2. **文档**: 全面的用户指南和示例
3. **监控**: 补丁指标的新仪表板
4. **支持**: 更新的故障排除指南

## 参考文献

- [OpenKruise Advanced DaemonSet文档](https://openkruise.io/docs/user-manuals/advanceddaemonset)
- [Kubernetes策略合并补丁](https://kubernetes.io/docs/tasks/manage-kubernetes-objects/update-api-object-kubectl-patch/)
- [WorkloadSpread设计](https://openkruise.io/docs/user-manuals/workloadspread)
