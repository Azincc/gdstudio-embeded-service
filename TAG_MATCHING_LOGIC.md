# 曲目元数据处理逻辑

本文描述下载任务与元数据编辑接口当前使用的元数据规则。

## 1. 数据来源

服务只使用以下数据：

1. 客户端创建下载任务时提交的曲名、艺术家、专辑、曲号和年份。
2. GDStudio 对当前音乐源返回的曲目信息、封面标识和歌词标识。
3. 音频文件中已经存在的标签，以及用户在元数据编辑界面提交的内容。

服务不再查询 MusicBrainz、Cover Art Archive 或 AcoustID，也不再生成音频指纹。

## 2. 下载任务的元数据规则

`stageTagging` 始终调用 GDMusic（GDStudio API）获取当前音乐源的曲目信息。查询成功后，返回的非空字段会覆盖任务中已有字段：

- `title`、`artist`、`album` 使用 GDMusic 返回的非空值。
- `track_number`、`year` 使用 GDMusic 返回的正整数值。
- `pic_id`、`lyric_id` 优先使用 GDMusic 返回值。

GDMusic 查询失败时按 `1s、2s、4s、8s、16s、30s...` 指数退避，单次等待最长 30 秒，总重试窗口最长 3 分钟。超过窗口仍未成功时，tagging 阶段失败，不再静默使用任务中的旧元数据。

封面与歌词仍通过 GDMusic 使用当前音乐源的 `pic_id`、`lyric_id` 获取。失败时记录警告，但不会让下载任务失败。

## 3. Album Artist

Album Artist 不再通过外部元数据服务查询。

- 多艺术家文本按 `/`、`,`、`;`、`、` 分隔，取第一个非空艺术家。
- 单艺术家文本直接使用原值。
- 空 Artist 对应空 Album Artist。

该值仅作为本地标签和目录路径的稳定兜底，不再跨任务传播外部匹配结果。

## 4. 元数据候选接口

候选接口按以下顺序处理：

1. 读取音频文件当前标签；读取失败时使用请求中的歌曲信息。
2. 合并同名 `.lrc` 歌词。
3. 分别通过 GDMusic 的 `netease`、`kuwo` 来源搜索候选。
4. 去重并返回当前值与候选值。

`netease` 和 `kuwo` 是两个独立来源，会并行执行各自的指数退避查询，每个来源最长重试 3 分钟。只有两个来源都成功后才返回结果；响应会保留两个独立候选对象，即使它们的元数据内容相同也不会跨来源去重。任一来源在窗口耗尽后仍失败，候选任务进入 `failed`，同步接口返回错误。

异步任务状态为：

1. `queued`
2. `searching_song`
3. `merging_data`
4. `done` 或 `failed`

不再返回 `matching_fingerprint`，候选来源中也不再出现 `musicbrainz_fingerprint` 或 `musicbrainz_search`。

## 5. 标签写入

服务继续写入常规标签：

- Title、Artist、Album Artist、Album
- Track Number、Disc Number、Year/Date
- Genre、Composer、Label、Comment
- Cover、Lyrics、Lyrics Translation

服务不再生成或写入 MusicBrainz Recording、Release、Release Group ID。编辑已有文件时不会主动删除文件中原有的 MusicBrainz 标签。
