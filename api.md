> ## Documentation Index
> Fetch the complete documentation index at: https://platform.minimax.io/docs/llms.txt
> Use this file to discover all available pages before exploring further.

# Music Generation

> Use this API to generate a song from lyrics and a prompt.



## OpenAPI

````yaml POST /v1/music_generation
openapi: 3.1.0
info:
  title: MiniMax Music Generation API
  description: >-
    MiniMax music generation API with support for creating music from text
    prompts and lyrics
  license:
    name: MIT
  version: 1.0.0
servers:
  - url: https://api.minimax.io
security:
  - bearerAuth: []
paths:
  /v1/music_generation:
    post:
      tags:
        - Music
      summary: Music Generation
      operationId: generateMusic
      parameters:
        - name: Content-Type
          in: header
          required: true
          description: >-
            The media type of the request body. Must be set to
            `application/json` to ensure the data is sent in JSON format.
          schema:
            type: string
            enum:
              - application/json
            default: application/json
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/GenerateMusicReq'
        required: true
      responses:
        '200':
          description: ''
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/GenerateMusicResp'
components:
  schemas:
    GenerateMusicReq:
      type: object
      required:
        - model
      properties:
        model:
          type: string
          description: >-
            The model name. Options:


            - `music-3.0` (recommended): Text-to-music generation. Available to
            Token Plan and paid users only, with an RPM of 120.

            - `music-2.6`: Previous-generation text-to-music model. Available to
            Token Plan and paid users only, with an RPM of 120.

            - `music-cover`: Cover generation from a reference audio. Available
            to Token Plan and paid users only, with an RPM of 120.

            - `music-3.0-free`: Free-tier version of `music-3.0`. Available to
            all users via API Key, with an RPM of 3.

            - `music-2.6-free`: Free-tier version of `music-2.6`. Available to
            all users via API Key, with an RPM of 3.

            - `music-cover-free`: Free-tier version of `music-cover`. Available
            to all users via API Key, with an RPM of 3.
          enum:
            - music-3.0
            - music-2.6
            - music-cover
            - music-3.0-free
            - music-2.6-free
            - music-cover-free
        prompt:
          type: string
          description: >-
            A description of the music, specifying style, mood, and scenario.


            For example: "`Pop, melancholic, perfect for a rainy night`".

            <br>

            Note:

            - For `music-3.0` / `music-3.0-free` / `music-2.6` /
            `music-2.6-free` with `is_instrumental: true`: Required. Length:
            1–2000 characters.

            - For `music-3.0` / `music-3.0-free` / `music-2.6` /
            `music-2.6-free` (non-instrumental): Optional. Length: 0–2000
            characters.

            - For `music-cover` / `music-cover-free`: Required. Describes the
            target cover style. Length: 10–300 characters.
          maxLength: 2000
        lyrics:
          type: string
          description: >-
            Song lyrics, using `\n` to separate lines. Supports structure tags:
            `[Intro]`, `[Verse]`, `[Pre Chorus]`, `[Chorus]`, `[Interlude]`,
            `[Bridge]`, `[Outro]`, `[Post Chorus]`, `[Transition]`, `[Break]`,
            `[Hook]`, `[Build Up]`, `[Inst]`, `[Solo]`.

            <br>

            Note:

            - For `music-3.0` / `music-3.0-free` / `music-2.6` /
            `music-2.6-free` with `is_instrumental: true`: Not required.

            - For `music-3.0` / `music-3.0-free` / `music-2.6` /
            `music-2.6-free` (non-instrumental): Required. Length: 1–3500
            characters.

            - For `music-cover` / `music-cover-free`: Optional. If omitted,
            lyrics are automatically extracted from the reference audio via ASR.
            Length: 10–1000 characters.

            - When `lyrics_optimizer: true` and `lyrics` is empty, the system
            will auto-generate lyrics from `prompt`.
          minLength: 1
          maxLength: 3500
        stream:
          type: boolean
          description: Whether to use streaming output.
          default: false
        output_format:
          type: string
          description: |-
            The output format of the audio. Options: `url` or `hex`.

            When `stream` is `true`, only `hex` is supported.

            ⚠️ Note: `url` links expire after 24 hours, so download promptly.
          enum:
            - url
            - hex
          default: hex
        audio_setting:
          $ref: '#/components/schemas/AudioSetting'
        lyrics_optimizer:
          type: boolean
          description: >-
            Whether to automatically generate lyrics based on the `prompt`
            description. Only supported on `music-3.0` / `music-3.0-free` /
            `music-2.6` / `music-2.6-free`.


            When set to `true` and `lyrics` is empty, the system will
            automatically generate lyrics from the prompt. Default: `false`.
          default: false
        is_instrumental:
          type: boolean
          description: >-
            Whether to generate instrumental music (no vocals). Only supported
            on `music-3.0` / `music-3.0-free` / `music-2.6` / `music-2.6-free`.


            When set to `true`, the `lyrics` field is not required. Default:
            `false`.
          default: false
        audio_url:
          type: string
          description: >-
            URL of the reference audio. Only used with `music-cover` /
            `music-cover-free` model. Exactly one of `audio_url` or
            `audio_base64` must be provided. Mutually exclusive with
            `cover_feature_id`.


            Reference audio constraints:

            - Duration: 6 seconds to 6 minutes

            - Size: max 50 MB

            - Format: common audio formats (mp3, wav, flac, etc.)
        audio_base64:
          type: string
          description: >-
            Base64-encoded reference audio. Only used with `music-cover` /
            `music-cover-free` model. Exactly one of `audio_url` or
            `audio_base64` must be provided. Mutually exclusive with
            `cover_feature_id`.


            Reference audio constraints:

            - Duration: 6 seconds to 6 minutes

            - Size: max 50 MB

            - Format: common audio formats (mp3, wav, flac, etc.)
        cover_feature_id:
          type: string
          description: >-
            Feature ID returned by the [Music Cover
            Preprocess](/api-reference/music-cover-preprocess) API. Used in the
            **two-step cover workflow** to generate a cover with modified
            lyrics.


            Only used with `music-cover` / `music-cover-free` model. Mutually
            exclusive with `audio_url` and `audio_base64`.


            - When provided, `lyrics` is required (length: 10–1000 characters)

            - The `cover_feature_id` is valid for 24 hours

            - Same audio content returns the same `cover_feature_id`
      example:
        model: music-3.0
        prompt: >-
          Indie folk, melancholic, introspective, longing, solitary walk, coffee
          shop
        lyrics: |-
          [verse]
          Streetlights flicker, the night breeze sighs
          Shadows stretch as I walk alone
          An old coat wraps my silent sorrow
          Wandering, longing, where should I go
          [chorus]
          Pushing the wooden door, the aroma spreads
          In a familiar corner, a stranger gazes
        audio_setting:
          sample_rate: 44100
          bitrate: 256000
          format: mp3
    GenerateMusicResp:
      type: object
      properties:
        data:
          $ref: '#/components/schemas/MusicData'
        base_resp:
          $ref: '#/components/schemas/BaseResp'
      example:
        data:
          audio: hex-encoded audio data
          status: 2
        trace_id: 04ede0ab069fb1ba8be5156a24b1e081
        extra_info:
          music_duration: 25364
          music_sample_rate: 44100
          music_channel: 2
          bitrate: 256000
          music_size: 813651
        analysis_info: null
        base_resp:
          status_code: 0
          status_msg: success
    AudioSetting:
      type: object
      description: Audio output configuration
      properties:
        sample_rate:
          type: integer
          description: 'Sampling rate. Options: `16000`, `24000`, `32000`, `44100`.'
        bitrate:
          type: integer
          description: 'Bitrate. Options: `32000`, `64000`, `128000`, `256000`.'
        format:
          type: string
          description: 'Audio format. Options: `mp3`, `wav`, `pcm`.'
          enum:
            - mp3
            - wav
            - pcm
    MusicData:
      type: object
      properties:
        status:
          type: integer
          description: |-
            Music generation status:

            1: In progress

            2: Completed
        audio:
          type: string
          description: |-
            Returned when `output_format` is `hex`.

            Contains the audio file as a hexadecimal-encoded string.
    BaseResp:
      type: object
      description: Status code and details
      properties:
        status_code:
          type: integer
          description: >-
            Status codes and their meanings:


            `0`: Success


            `1002`: Rate limit triggered, retry later


            `1004`: Authentication failed, check API Key


            `1008`: Insufficient balance


            `1026`: Content flagged for sensitive material


            `2013`: Invalid parameters, check input


            `2049`: Invalid API Key


            For more information, please refer to the [Error Code
            Reference](/api-reference/errorcode).
        status_msg:
          type: string
          description: Detailed error message
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
      bearerFormat: JWT
      description: >-
        `HTTP: Bearer Auth`

        - Security Scheme Type: http

        - HTTP Authorization Scheme: `Bearer API_key`, can be found in [Account
        Management>API
        Keys](https://platform.minimax.io/user-center/basic-information/interface-key).

````