# kube-upgrade-preflight

[![ci](https://github.com/OTA-EITA/kube-upgrade-preflight/actions/workflows/ci.yml/badge.svg)](https://github.com/OTA-EITA/kube-upgrade-preflight/actions/workflows/ci.yml)

Kubernetes のバージョンアップを**始める前に**、作業を止めたり壊したりする原因をまとめて洗い出す CLI です。
ノードを 1 台ずつドレインしていくローリングアップデートを**メモリ上で dry-run** し、クラスタには一切変更を加えません。

A CLI that finds what would block or break a Kubernetes upgrade before you start it,
including an in-memory dry-run of draining every node in turn. It never changes the cluster.

作業後の確認には姉妹ツールの [kube-postcheck](https://github.com/OTA-EITA/kube-postcheck) を使います。

```
kube-upgrade-preflight --target v1.35   # 作業前: 止まる原因がないか
(コントロールプレーン → ノードの順にアップデート)
kube-postcheck                          # 作業後: 正常に戻ったか
```

## なぜ作ったか

EKS などのバージョンアップでは、次のような「始めてから気づく」問題で作業が止まりがちです。

- ドレインが PodDisruptionBudget で無限に待ち続ける
- 削除された API を使う Helm リリースが、アップデート後に `helm upgrade` できなくなる
- 退避した Pod の行き先が足りず Pending のまま残る
- バージョンを飛ばしてしまい、スキューポリシー違反になる

これらを事前に 1 コマンドで確認し、作業可否の判断と手順書の証跡に使えるようにしました。

## チェック項目

| 名前 | 内容 |
|---|---|
| `version-skew` | コントロールプレーンが 1 マイナーずつ上がるか、アップデート後も全ノードの kubelet がスキューポリシー（3 マイナー以内）に収まるか |
| `deprecated-apis` | API サーバーのメトリクス `apiserver_requested_deprecated_apis` から、ターゲットで削除される API を**実際に呼んでいるクライアント**を検出 |
| `helm-releases` | Helm 3 リリースの最新リビジョンのマニフェストを復号し、ターゲットで削除される apiVersion を検出 |
| `pdb` | 許容停止数が 0 の PodDisruptionBudget（ドレインが止まる）、対象 Pod のない PDB |
| `single-replica` | レプリカ 1 の Deployment / StatefulSet（ドレイン中に停止する） |
| `drain-dry-run` | ローリングアップデートのシミュレーション（下記） |

### drain-dry-run: アップデートの論理的な dry-run

`kubectl drain` を全ノードに順番に実行した場合を、クラスタのスナップショットからメモリ上で再現します。

1. コントロールプレーン → ワーカーの順に、ノードを 1 台ずつ cordon する
2. DaemonSet と static Pod を除く Pod を退避する
3. 退避した Pod を、残りのノードに**スケジューラと同じ条件**で配置し直す
   - CPU / メモリ / Pod 数の requests（init コンテナ、sidecar、Pod overhead を含む）
   - nodeSelector と必須の nodeAffinity
   - NoSchedule / NoExecute の taint と toleration
   - 必須の Pod anti-affinity（「1 ノードに 1 レプリカ」やゾーン分散。既存 Pod 側のルールも双方向に評価）
   - cordon 中・NotReady のノードは使わない
4. ノードを uncordon して次のノードへ（前のノードから移った Pod は、移動先のノードの番で再び退避される）

各ノードについて次を判定します。

| 判定 | 内容 |
|---|---|
| FAIL | 許容停止数 0 の PDB に守られた Pod がいる（ドレインが止まる） |
| FAIL | 退避した Pod の行き先がない（理由を `0/3 nodes are available: 1 cordoned, 1 insufficient memory, ...` の形式で表示） |
| FAIL | コントローラを持たない Pod がいる（`--force` が必要で、二度と戻らない） |
| WARN | 1 ノード上の Pod 数が PDB の許容停止数を超える（ドレインが置き換え待ちで遅くなる） |
| WARN | emptyDir を使う Pod がいる（データが消える） |

スケジューラの判定には Kubernetes 公式の [`k8s.io/component-helpers`](https://github.com/kubernetes/component-helpers) を使っています。
必須の Pod affinity、topology spread、ボリュームのゾーン制約はモデル化していないため、PASS は「リソース・ノード・anti-affinity の制約上は収まる」という意味です。

#### `--server-dry-run`: API サーバーによる確認

`--server-dry-run` を付けると、退避対象の各 Pod について Eviction API を `dryRun=All` で実際に呼び出します。
Pod は退避されず、PDB による拒否（HTTP 429）を API サーバー自身の判定で確認できます。`pods/eviction` の create 権限が必要です。

## インストール

[Releases](https://github.com/OTA-EITA/kube-upgrade-preflight/releases) から OS に合ったバイナリをダウンロードするか、Go でインストールします。

```sh
go install github.com/OTA-EITA/kube-upgrade-preflight/cmd/kube-upgrade-preflight@latest
```

コンテナイメージもあります（distroless、非 root で動作）。

```sh
docker run --rm -v ~/.kube:/home/nonroot/.kube:ro ghcr.io/ota-eita/kube-upgrade-preflight:latest --help
```

## 使い方

```sh
# v1.35 へのアップデートを事前チェック
kube-upgrade-preflight --target v1.35

# API サーバーの dry-run も行い、問題のある項目だけ表示
kube-upgrade-preflight --target v1.35 --server-dry-run --quiet

# 作業手順書の証跡として保存
kube-upgrade-preflight --context prod --target v1.35 --output markdown > preflight-$(date +%Y%m%d).md
```

出力例（`--quiet`）:

```
STATUS  CHECK           RESOURCE                 MESSAGE
FAIL    version-skew    node/worker-old          kubelet v1.31 would be 4 minors behind v1.35 (max 3); upgrade this node first
FAIL    helm-releases   helm/web/legacy (rev 4)  Ingress legacy uses extensions/v1beta1, removed in v1.22; migrate to networking.k8s.io/v1
FAIL    helm-releases   helm/web/legacy (rev 4)  PodDisruptionBudget legacy uses policy/v1beta1, removed in v1.25; migrate to policy/v1
FAIL    pdb             pdb/app/db               allows 0 disruptions (1 healthy, 1 required); drains will block
FAIL    drain-dry-run   node/worker-1            PDB app/db allows 0 disruptions; evicting 1 pod(s) here blocks the drain
FAIL    drain-dry-run   node/worker-1            cannot reschedule while this node is drained: app/batch-7c9 (0/3 nodes are available: 1 cordoned, 1 insufficient memory, 1 untolerated taint node-role.kubernetes.io/control-plane)
FAIL    drain-dry-run   node/worker-old          PDB app/db allows 0 disruptions; evicting 1 pod(s) here blocks the drain
WARN    single-replica  statefulset/app/db       1 replica; unavailable while its node is drained

target=v1.35 overall=FAIL pass=5 warn=1 fail=7 errors=0
```

### 主なオプション

| フラグ | 既定値 | 説明 |
|---|---|---|
| `--target` | （必須） | アップデート先のバージョン（`v1.35`、`1.35` など） |
| `--server-dry-run` | `false` | Eviction API を dryRun で呼んで確認する |
| `--kubeconfig` / `--context` | `$KUBECONFIG` / 現在のコンテキスト | 接続先 |
| `--output` | `table` | `table` / `json` / `markdown` |
| `--fail-on` | `fail` | `warn` にすると WARN でも終了コード 1 |
| `--namespace` / `--exclude-namespace` | 全 namespace | 報告対象の絞り込み（シミュレーション自体は全 Pod で行う） |
| `--only` / `--skip` | - | 実行するチェックの選択 |
| `--list` | - | チェック一覧を表示 |

終了コードは 0（問題なし）、1（FAIL あり、またはチェック不能）、2（引数や kubeconfig の誤り）です。

## 必要な権限

[`deploy/rbac.yaml`](deploy/rbac.yaml) に最小権限の ClusterRole を用意しています。
`--server-dry-run` を使わない場合は、すべて読み取りのみです。

## テスト

```sh
make test   # go test -race（fake clientset によるテーブルテスト）
make lint   # golangci-lint
```

CI では kind で 3 ノードのクラスタを作り、[`test/e2e/run.sh`](test/e2e/run.sh) で次を確認しています。

1. 正常なクラスタで FAIL にならないこと
2. ドレインを止める PDB とコントローラのない Pod を置くと FAIL になり、`--server-dry-run` で API サーバーが退避を拒否すること

## License

MIT
