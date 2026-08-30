# Custom icon set

このディレクトリは、フォーク先や社内環境でライセンスを確認したSVG
アイコンを配置するためのスペースです。公式ベンダーアイコンは
`oekaki` 本体には同梱していません。

`oekaki render --icon-dir ./icons -f html ...` を実行すると、グラフに
含まれるリソースだけが読み込まれます。ファイル名は次の規則です。

```text
<Terraform resource type>.svg   # 例: aws_ecs_service.svg
<category>.svg                  # compute / database / network / security / storage / generic
```

リソース種別の完全一致を優先し、該当するファイルがなければカテゴリ用
アイコン、さらに本体内蔵のカテゴリ用グリフへフォールバックします。

例：

```text
icons/
├── aws_ecs_service.svg
├── aws_db_instance.svg
├── aws_vpc.svg
├── sakura_server.svg
├── kubernetes_deployment.svg
└── compute.svg
```

SVGの利用条件・出典・バージョンは、各フォーク先の責任で確認して
管理してください。ここに置いたアイコンはHTML出力に埋め込まれるため、
生成物を共有する場合も同じ利用条件が適用されるか確認してください。
