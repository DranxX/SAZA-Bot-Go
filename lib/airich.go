package lib

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	waBinary "go.mau.fi/whatsmeow/binary"
	waAICommon "go.mau.fi/whatsmeow/proto/waAICommon"
	depr "go.mau.fi/whatsmeow/proto/waAICommonDeprecated"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

var mediaStoreCache sync.Map

type Messager interface {
	SendMessage(ctx context.Context, to types.JID, msg *waProto.Message, extra ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error)
}

type mediaUploadJob struct {
	kind      string
	sourceURL string
	extraInfo map[string]any
}

type RichComposer struct {
	agent       Messager
	headerTitle string
	mainBody    string
	footerText  string
	parts       []*msgSegment
	layouts     []any
	citations   []any
	mentions    []string
	queuedMedia []mediaUploadJob
}

type msgSegment struct {
	segmentType   depr.AIRichResponseSubMessageType
	textContent   *string
	codeBlock     *depr.AIRichResponseCodeMetadata
	tableData     *depr.AIRichResponseTableMetadata
	imageGrid     *depr.AIRichResponseGridImageMetadata
	carouselItems *depr.AIRichResponseContentItemsMetadata
}

type embeddedEntity struct {
	Token string         `json:"key"`
	Info  map[string]any `json:"metadata"`
}

func NewComposer(agent Messager) *RichComposer {
	return &RichComposer{agent: agent}
}

func (r *RichComposer) WithHeader(s string) *RichComposer {
	r.headerTitle = s
	return r
}

func (r *RichComposer) WithBody(s string) *RichComposer {
	r.mainBody = s
	return r
}

func (r *RichComposer) WithFooter(s string) *RichComposer {
	r.footerText = s
	return r
}

func (r *RichComposer) pushTextSegment(text string) *RichComposer {
	extractedText, entities := parseInlineEntities(text)

	r.parts = append(r.parts, &msgSegment{
		segmentType: depr.AIRichResponseSubMessageType_AI_RICH_RESPONSE_TEXT,
		textContent: proto.String(extractedText),
	})

	data := map[string]any{
		"text":       extractedText,
		"__typename": "GenAIMarkdownTextUXPrimitive",
	}
	if len(entities) > 0 {
		data["inline_entities"] = entities
	}
	r.layouts = append(r.layouts, buildLayoutData("Single", data, nil))

	return r
}

func (r *RichComposer) AppendText(text string) *RichComposer {
	const limit = 950
	if len(text) > limit {
		paragraphs := strings.Split(text, "\n")
		var buffer strings.Builder
		for _, p := range paragraphs {
			if buffer.Len()+len(p) > limit {
				trimmed := strings.TrimSpace(buffer.String())
				if trimmed != "" {
					r.pushTextSegment(trimmed)
				}
				buffer.Reset()
				buffer.WriteString(p + "\n")
			} else {
				buffer.WriteString(p + "\n")
			}
		}
		trimmed := strings.TrimSpace(buffer.String())
		if trimmed != "" {
			r.pushTextSegment(trimmed)
		}
		return r
	}
	return r.pushTextSegment(text)
}

func (r *RichComposer) AppendCode(language, code string) *RichComposer {
	protoBlocks, unifiedBlocks := tokenizeCodeString(code, language)
	r.parts = append(r.parts, &msgSegment{
		segmentType: depr.AIRichResponseSubMessageType_AI_RICH_RESPONSE_CODE,
		codeBlock: &depr.AIRichResponseCodeMetadata{
			CodeLanguage: proto.String(language),
			CodeBlocks:   protoBlocks,
		},
	})

	r.layouts = append(r.layouts, buildLayoutData("Single", map[string]any{
		"language":    language,
		"code_blocks": unifiedBlocks,
		"__typename":  "GenAICodeUXPrimitive",
	}, nil))

	return r
}

func (r *RichComposer) AppendTable(table [][]string) *RichComposer {
	if len(table) == 0 {
		return r
	}
	title, protoRows, unifiedRows := formatTableMetadata(table)

	r.parts = append(r.parts, &msgSegment{
		segmentType: depr.AIRichResponseSubMessageType_AI_RICH_RESPONSE_TABLE,
		tableData: &depr.AIRichResponseTableMetadata{
			Title: proto.String(title),
			Rows:  protoRows,
		},
	})

	r.layouts = append(r.layouts, buildLayoutData("Single", map[string]any{
		"rows":       unifiedRows,
		"__typename": "GenATableUXPrimitive",
	}, nil))

	return r
}

func (r *RichComposer) AppendCitations(sources [][]string) *RichComposer {
	var list []map[string]any
	for _, s := range sources {
		icon := ""
		url := ""
		text := ""
		if len(s) > 0 {
			icon = s[0]
		}
		if len(s) > 1 {
			url = s[1]
		}
		if len(s) > 2 {
			text = s[2]
		}
		list = append(list, map[string]any{
			"source_type":         "THIRD_PARTY",
			"source_display_name": text,
			"source_subtitle":     "AI",
			"source_url":          url,
			"favicon": map[string]any{
				"url":       icon,
				"mime_type": "image/jpeg",
				"width":     16,
				"height":    16,
			},
		})
	}

	r.layouts = append(r.layouts, buildLayoutData("Single", map[string]any{
		"sources":    list,
		"__typename": "GenAISearchResultPrimitive",
	}, nil))

	return r
}

func (r *RichComposer) AppendReels(reelsItems []map[string]any) *RichComposer {
	var protoReels []*depr.AIRichResponseContentItemsMetadata_AIRichResponseContentItemMetadata
	var sectionReels []map[string]any

	for idx, item := range reelsItems {
		username, _ := item["username"].(string)
		profileIcon, _ := item["profileIconUrl"].(string)
		if profileIcon == "" {
			profileIcon, _ = item["profile_url"].(string)
		}
		if profileIcon == "" {
			profileIcon, _ = item["profile"].(string)
		}

		thumbnail, _ := item["thumbnailUrl"].(string)
		if thumbnail == "" {
			thumbnail, _ = item["thumbnail"].(string)
		}

		videoURL, _ := item["videoUrl"].(string)
		if videoURL == "" {
			videoURL, _ = item["url"].(string)
		}

		reelProto := &depr.AIRichResponseContentItemsMetadata_AIRichResponseReelItem{
			Title:          proto.String(username),
			ProfileIconURL: proto.String(profileIcon),
			ThumbnailURL:   proto.String(thumbnail),
			VideoURL:       proto.String(videoURL),
		}

		protoReels = append(protoReels, &depr.AIRichResponseContentItemsMetadata_AIRichResponseContentItemMetadata{
			AIRichResponseContentItem: &depr.AIRichResponseContentItemsMetadata_AIRichResponseContentItemMetadata_ReelItem{
				ReelItem: reelProto,
			},
		})

		r.citations = append(r.citations, &waAICommon.BotSourcesMetadata_BotSourceItem{
			Provider:          waAICommon.BotSourcesMetadata_BotSourceItem_OTHER.Enum(),
			ThumbnailCDNURL:   proto.String(thumbnail),
			SourceProviderURL: proto.String(videoURL),
			SourceQuery:       proto.String(""),
			FaviconCDNURL:     proto.String(profileIcon),
			CitationNumber:    proto.Uint32(uint32(idx + 1)),
			SourceTitle:       proto.String(username),
		})

		likes, _ := item["likes_count"].(float64)
		if likes == 0 {
			if l, ok := item["like"].(float64); ok {
				likes = l
			}
		}
		shares, _ := item["shares_count"].(float64)
		if shares == 0 {
			if s, ok := item["share"].(float64); ok {
				shares = s
			}
		}
		views, _ := item["view_count"].(float64)
		if views == 0 {
			if v, ok := item["view"].(float64); ok {
				views = v
			}
		}
		reelSource, _ := item["reel_source"].(string)
		if reelSource == "" {
			if s, ok := item["source"].(string); ok {
				reelSource = s
			}
			if reelSource == "" {
				reelSource = "IG"
			}
		}
		isVerified, _ := item["is_verified"].(bool)
		if !isVerified {
			if v, ok := item["verified"].(bool); ok {
				isVerified = v
			}
		}
		title, _ := item["title"].(string)
		reelsTitle, _ := item["reels_title"].(string)
		if reelsTitle == "" {
			reelsTitle = title
		}

		sectionReels = append(sectionReels, map[string]any{
			"reels_url":     videoURL,
			"thumbnail_url": thumbnail,
			"creator":       username,
			"avatar_url":    profileIcon,
			"reels_title":   reelsTitle,
			"likes_count":   likes,
			"shares_count":  shares,
			"view_count":    views,
			"reel_source":   reelSource,
			"is_verified":   isVerified,
			"__typename":    "GenAIReelPrimitive",
		})
	}

	r.parts = append(r.parts, &msgSegment{
		segmentType: depr.AIRichResponseSubMessageType_AI_RICH_RESPONSE_CONTENT_ITEMS,
		carouselItems: &depr.AIRichResponseContentItemsMetadata{
			ContentType:   depr.AIRichResponseContentItemsMetadata_CAROUSEL.Enum(),
			ItemsMetadata: protoReels,
		},
	})

	r.layouts = append(r.layouts, buildLayoutData("HScroll", sectionReels, nil))

	return r
}

func (r *RichComposer) AppendImages(imageURL any) *RichComposer {
	var urls []string
	switch v := imageURL.(type) {
	case string:
		urls = []string{v}
	case []string:
		urls = v
	default:
		return r
	}

	for _, u := range urls {
		r.queuedMedia = append(r.queuedMedia, mediaUploadJob{
			kind:      "image",
			sourceURL: u,
		})
	}
	return r
}

func (r *RichComposer) AppendVideos(videoURL any) *RichComposer {
	var items []map[string]any
	switch v := videoURL.(type) {
	case string:
		items = []map[string]any{{"url": v}}
	case []string:
		for _, u := range v {
			items = append(items, map[string]any{"url": u})
		}
	case map[string]any:
		items = []map[string]any{v}
	case []map[string]any:
		items = v
	default:
		return r
	}

	for _, item := range items {
		url, _ := item["url"].(string)
		if url == "" {
			continue
		}
		r.queuedMedia = append(r.queuedMedia, mediaUploadJob{
			kind:      "video",
			sourceURL: url,
			extraInfo: item,
		})
	}
	return r
}

func (r *RichComposer) executeMediaQueue(ctx context.Context) {
	if len(r.queuedMedia) == 0 {
		return
	}

	var imageUrls []string

	for _, job := range r.queuedMedia {
		resolvedURL := job.sourceURL
		if ctx != nil && r.agent != nil {
			cli, ok := r.agent.(*whatsmeow.Client)
			if ok {
				waMediaType := whatsmeow.MediaImage
				if job.kind != "image" {
					waMediaType = whatsmeow.MediaVideo
				}
				if uploaded, err := transmitNewsletterMediaAndCache(ctx, cli, job.sourceURL, waMediaType); err == nil && uploaded.URL != "" {
					resolvedURL = uploaded.URL
				}
			}
		}

		if job.kind == "image" {
			imageUrls = append(imageUrls, resolvedURL)

			r.layouts = append(r.layouts, buildLayoutData("Single", map[string]any{
				"media": map[string]any{
					"url":       resolvedURL,
					"mime_type": "image/png",
				},
				"imagine_type": "IMAGE",
				"status": map[string]any{
					"status": "READY",
				},
				"__typename": "GenAIImaginePrimitive",
			}, nil))
		} else {
			r.parts = append(r.parts, &msgSegment{
				segmentType: depr.AIRichResponseSubMessageType_AI_RICH_RESPONSE_TEXT,
				textContent: proto.String("[ CANNOT_LOAD_VIDEO - WA_ ]"),
			})

			mimeType, _ := job.extraInfo["mime_type"].(string)
			if mimeType == "" {
				mimeType = "video/mp4"
			}
			fileLength, _ := job.extraInfo["file_length"].(float64)
			duration, _ := job.extraInfo["duration"].(float64)
			thumbnail, _ := job.extraInfo["thumbnail"].(string)

			mediaMap := map[string]any{
				"url":       resolvedURL,
				"mime_type": mimeType,
			}
			if fileLength > 0 {
				mediaMap["file_length"] = fileLength
			}
			if duration > 0 {
				mediaMap["duration"] = duration
			}

			primitive := map[string]any{
				"media":        mediaMap,
				"imagine_type": "ANIMATE",
				"status": map[string]any{
					"status": "READY",
				},
				"__typename": "GenAIImaginePrimitive",
			}
			if thumbnail != "" {
				resolvedThumb := thumbnail
				if ctx != nil && r.agent != nil {
					cli, ok := r.agent.(*whatsmeow.Client)
					if ok {
						if uploaded, err := transmitNewsletterMediaAndCache(ctx, cli, thumbnail, whatsmeow.MediaImage); err == nil && uploaded.URL != "" {
							resolvedThumb = uploaded.URL
						}
					}
				}
				primitive["thumbnail"] = map[string]any{
					"raw_media": resolvedThumb,
				}
			}

			r.layouts = append(r.layouts, buildLayoutData("Single", primitive, nil))
		}
	}

	if len(imageUrls) > 0 {
		var protoURLs []*depr.AIRichResponseImageURL
		for _, u := range imageUrls {
			protoURLs = append(protoURLs, &depr.AIRichResponseImageURL{
				ImagePreviewURL: proto.String(u),
				ImageHighResURL: proto.String(u),
				SourceURL:       proto.String(u),
			})
		}

		r.parts = append(r.parts, &msgSegment{
			segmentType: depr.AIRichResponseSubMessageType_AI_RICH_RESPONSE_GRID_IMAGE,
			imageGrid: &depr.AIRichResponseGridImageMetadata{
				GridImageURL: &depr.AIRichResponseImageURL{
					ImagePreviewURL: proto.String(imageUrls[0]),
				},
				ImageURLs: protoURLs,
			},
		})
	}

	r.queuedMedia = nil
}

func (r *RichComposer) AppendProduct(title, brand, price, salePrice, imageURL, productURL string) *RichComposer {
	r.parts = append(r.parts, &msgSegment{
		segmentType: depr.AIRichResponseSubMessageType_AI_RICH_RESPONSE_TEXT,
		textContent: proto.String("[ CANNOT_LOAD_PRODUCT - WA_ ]"),
	})

	productMap := map[string]any{
		"title":       title,
		"brand":       brand,
		"price":       price,
		"sale_price":  salePrice,
		"product_url": productURL,
		"image": map[string]any{
			"url": imageURL,
		},
		"additional_images": []map[string]any{
			{
				"url": imageURL,
			},
		},
		"__typename": "GenAIProductItemCardPrimitive",
	}

	r.layouts = append(r.layouts, buildLayoutData("Single", map[string]any{
		"text":       "\n\n",
		"__typename": "GenAIMarkdownTextUXPrimitive",
	}, nil))

	r.layouts = append(r.layouts, buildLayoutData("Single", productMap, nil))

	r.layouts = append(r.layouts, buildLayoutData("Single", map[string]any{
		"text":       "\n\n",
		"__typename": "GenAIMarkdownTextUXPrimitive",
	}, nil))

	return r
}

func (r *RichComposer) AppendPost(postData any) *RichComposer {
	var items []map[string]any
	switch v := postData.(type) {
	case map[string]any:
		items = []map[string]any{v}
	case []map[string]any:
		items = v
	default:
		return r
	}

	if len(items) == 0 {
		return r
	}

	r.parts = append(r.parts, &msgSegment{
		segmentType: depr.AIRichResponseSubMessageType_AI_RICH_RESPONSE_TEXT,
		textContent: proto.String("[ CANNOT_LOAD_POST - WA_ ]"),
	})

	var primitives []map[string]any
	for _, p := range items {
		title, _ := p["title"].(string)
		subtitle, _ := p["subtitle"].(string)
		username, _ := p["username"].(string)
		profilePicture, _ := p["profile_picture_url"].(string)
		if profilePicture == "" {
			profilePicture, _ = p["profile_url"].(string)
		}
		if profilePicture == "" {
			profilePicture, _ = p["profile"].(string)
		}

		isVerified, _ := p["is_verified"].(bool)
		if !isVerified {
			if v, ok := p["verified"].(bool); ok {
				isVerified = v
			}
		}

		thumbnail, _ := p["thumbnail_url"].(string)
		if thumbnail == "" {
			thumbnail, _ = p["thumbnail"].(string)
		}

		caption, _ := p["post_caption"].(string)
		if caption == "" {
			caption, _ = p["caption"].(string)
		}

		likes, _ := p["likes_count"].(float64)
		if likes == 0 {
			if l, ok := p["like"].(float64); ok {
				likes = l
			}
		}

		comments, _ := p["comments_count"].(float64)
		if comments == 0 {
			if c, ok := p["comment"].(float64); ok {
				comments = c
			}
		}

		shares, _ := p["shares_count"].(float64)
		if shares == 0 {
			if s, ok := p["share"].(float64); ok {
				shares = s
			}
		}

		postURL, _ := p["post_url"].(string)
		if postURL == "" {
			postURL, _ = p["url"].(string)
		}

		deeplink, _ := p["post_deeplink"].(string)
		if deeplink == "" {
			deeplink, _ = p["deeplink"].(string)
		}

		sourceApp, _ := p["source_app"].(string)
		if sourceApp == "" {
			sourceApp, _ = p["source"].(string)
		}
		if sourceApp == "" {
			sourceApp = "INSTAGRAM"
		}

		footerLabel, _ := p["footer_label"].(string)
		if footerLabel == "" {
			footerLabel, _ = p["footer"].(string)
		}

		footerIcon, _ := p["footer_icon"].(string)
		if footerIcon == "" {
			footerIcon, _ = p["icon"].(string)
		}

		orientation, _ := p["orientation"].(string)
		if orientation == "" {
			orientation = "LANDSCAPE"
		}

		postType, _ := p["post_type"].(string)
		if postType == "" {
			postType = "VIDEO"
		}

		primitives = append(primitives, map[string]any{
			"title":               title,
			"subtitle":            subtitle,
			"username":            username,
			"profile_picture_url": profilePicture,
			"is_verified":         isVerified,
			"thumbnail_url":       thumbnail,
			"post_caption":        caption,
			"likes_count":         likes,
			"comments_count":      comments,
			"shares_count":        shares,
			"post_url":            postURL,
			"post_deeplink":       deeplink,
			"source_app":          sourceApp,
			"footer_label":        footerLabel,
			"footer_icon":         footerIcon,
			"is_carousel":         len(items) > 1,
			"orientation":         orientation,
			"post_type":           postType,
			"__typename":          "GenAIPostPrimitive",
		})
	}

	r.layouts = append(r.layouts, buildLayoutData("HScroll", primitives, nil))

	return r
}

func (r *RichComposer) AppendTip(text string) *RichComposer {
	r.parts = append(r.parts, &msgSegment{
		segmentType: depr.AIRichResponseSubMessageType_AI_RICH_RESPONSE_TEXT,
		textContent: proto.String(text),
	})

	r.layouts = append(r.layouts, buildLayoutData("Single", map[string]any{
		"text":       text,
		"__typename": "GenAIMetadataTextPrimitive",
	}, nil))

	return r
}

func (r *RichComposer) AppendSuggestedPills(suggestion any) *RichComposer {
	var texts []string
	switch v := suggestion.(type) {
	case string:
		texts = []string{v}
	case []string:
		texts = v
	default:
		return r
	}

	if len(texts) == 0 {
		return r
	}

	var suggest []map[string]any
	for _, text := range texts {
		suggest = append(suggest, map[string]any{
			"prompt_text": text,
			"prompt_type": "SUGGESTED_PROMPT",
			"__typename":  "GenAIFollowUpSuggestionPillPrimitive",
		})
	}

	layoutType := "HScroll"
	if len(suggest) == 1 {
		layoutType = "Single"
	}

	var data any
	if layoutType == "Single" {
		data = suggest[0]
	} else {
		data = suggest
	}

	r.layouts = append(r.layouts, buildLayoutData(layoutType, data, map[string]any{
		"__typename": "GenAIUnifiedResponseSection",
	}))

	return r
}

func (r *RichComposer) MentionJIDs(jids []string) *RichComposer {
	r.mentions = append(r.mentions, jids...)
	return r
}

func (r *RichComposer) AppendSegment(sub *msgSegment) *RichComposer {
	if sub != nil {
		r.parts = append(r.parts, sub)
	}
	return r
}

func (r *RichComposer) AppendLayoutSection(section any) *RichComposer {
	if section != nil {
		r.layouts = append(r.layouts, section)
	}
	return r
}

func (r *RichComposer) AppendCustomLayout(name string, data any, extra map[string]any) *RichComposer {
	r.layouts = append(r.layouts, buildLayoutData(name, data, extra))
	return r
}

func (r *RichComposer) AssembleMessage(evt *events.Message) *waProto.Message {
	r.executeMediaQueue(nil)
	if r.headerTitle != "" || r.mainBody != "" || r.footerText != "" {
		var contentPieces []string
		if r.headerTitle != "" {
			contentPieces = append(contentPieces, "# "+r.headerTitle)
		}
		if r.mainBody != "" {
			contentPieces = append(contentPieces, r.mainBody)
		}
		if r.footerText != "" {
			contentPieces = append(contentPieces, "_"+r.footerText+"_")
		}
		combined := strings.Join(contentPieces, "\n\n")

		extractedText, entities := parseInlineEntities(combined)

		r.parts = append([]*msgSegment{{
			segmentType: depr.AIRichResponseSubMessageType_AI_RICH_RESPONSE_TEXT,
			textContent: proto.String(extractedText),
		}}, r.parts...)

		data := map[string]any{
			"text":       extractedText,
			"__typename": "GenAIMarkdownTextUXPrimitive",
		}
		if len(entities) > 0 {
			data["inline_entities"] = entities
		}
		r.layouts = append([]any{buildLayoutData("Single", data, nil)}, r.layouts...)
	}

	var protoSubs []*depr.AIRichResponseSubMessage
	for _, s := range r.parts {
		ps := &depr.AIRichResponseSubMessage{
			MessageType: s.segmentType.Enum(),
		}
		switch {
		case s.textContent != nil:
			ps.MessageText = s.textContent
		case s.codeBlock != nil:
			ps.CodeMetadata = s.codeBlock
		case s.tableData != nil:
			ps.TableMetadata = s.tableData
		case s.imageGrid != nil:
			ps.GridImageMetadata = s.imageGrid
		case s.carouselItems != nil:
			ps.ContentItemsMetadata = s.carouselItems
		}
		protoSubs = append(protoSubs, ps)
	}

	var responsePayload []byte
	if len(r.layouts) > 0 {
		payload := map[string]any{
			"response_id": generateCryptoUUID(),
			"sections":    r.layouts,
		}
		jsonData, err := json.Marshal(payload)
		if err == nil {
			responsePayload = jsonData
		}
	}

	richMsg := &waProto.AIRichResponseMessage{
		MessageType: depr.AIRichResponseMessageType_AI_RICH_RESPONSE_TYPE_STANDARD.Enum(),
		Submessages: protoSubs,
	}

	if len(responsePayload) > 0 {
		richMsg.UnifiedResponse = &waAICommon.AIRichResponseUnifiedResponse{
			Data: responsePayload,
		}
	}

	ci := &waProto.ContextInfo{
		IsForwarded:     proto.Bool(true),
		ForwardingScore: proto.Uint32(1),
		ForwardOrigin:   waProto.ContextInfo_META_AI.Enum(),
		ForwardedAiBotMessageInfo: &waAICommon.ForwardedAIBotMessageInfo{
			BotJID: proto.String("0@bot"),
		},
	}

	if evt != nil {
		ci.StanzaID = proto.String(evt.Info.ID)
		ci.Participant = proto.String(evt.Info.Sender.String())
		ci.QuotedMessage = evt.Message
	}

	if len(r.mentions) > 0 {
		ci.MentionedJID = r.mentions
	}

	richMsg.ContextInfo = ci

	botMetadata := &waAICommon.BotMetadata{
		MessageDisclaimerText: proto.String(r.headerTitle),
	}

	botMetadata.SessionTransparencyMetadata = &waAICommon.SessionTransparencyMetadata{
		DisclaimerText:          proto.String("~ Ahmad tumbuh kembang"),
		HcaID:                   proto.String(fmt.Sprintf("hca_%d", time.Now().UnixMilli())),
		SessionTransparencyType: waAICommon.SessionTransparencyType_NY_AI_SAFETY_DISCLAIMER.Enum(),
	}

	if len(r.citations) > 0 {
		var protoSources []*waAICommon.BotSourcesMetadata_BotSourceItem
		for _, s := range r.citations {
			if item, ok := s.(*waAICommon.BotSourcesMetadata_BotSourceItem); ok {
				protoSources = append(protoSources, item)
			}
		}
		botMetadata.RichResponseSourcesMetadata = &waAICommon.BotSourcesMetadata{
			Sources: protoSources,
		}
	}

	msgContextInfo := &waProto.MessageContextInfo{
		DeviceListMetadata:        &waProto.DeviceListMetadata{},
		DeviceListMetadataVersion: proto.Int32(2),
		BotMetadata:               botMetadata,
	}

	return &waProto.Message{
		MessageContextInfo: msgContextInfo,
		BotForwardedMessage: &waProto.FutureProofMessage{
			Message: &waProto.Message{
				RichResponseMessage: richMsg,
			},
		},
	}
}

func (r *RichComposer) DispatchMessage(ctx context.Context, to types.JID, evt *events.Message) error {
	r.executeMediaQueue(ctx)
	for i, sec := range r.layouts {
		r.layouts[i] = traverseAndProcessLayoutMedia(ctx, r.agent, sec)
	}
	msg := r.AssembleMessage(evt)

	additionalNodes := []waBinary.Node{
		{
			Tag:   "biz",
			Attrs: waBinary.Attrs{},
			Content: []waBinary.Node{
				{
					Tag:   "interactive",
					Attrs: waBinary.Attrs{"type": "native_flow", "v": "1"},
					Content: []waBinary.Node{
						{
							Tag:   "native_flow",
							Attrs: waBinary.Attrs{"v": "9", "name": "mixed"},
						},
					},
				},
			},
		},
	}

	_, err := r.agent.SendMessage(ctx, to, msg, whatsmeow.SendRequestExtra{
		AdditionalNodes: &additionalNodes,
	})
	return err
}

func (r *RichComposer) AppendTextSegment(text string) *RichComposer {
	r.parts = append(r.parts, &msgSegment{
		segmentType: depr.AIRichResponseSubMessageType_AI_RICH_RESPONSE_TEXT,
		textContent: &text,
	})
	return r
}

func (r *RichComposer) AppendCodeSegment(lang string, blocks []*depr.AIRichResponseCodeMetadata_AIRichResponseCodeBlock) *RichComposer {
	r.parts = append(r.parts, &msgSegment{
		segmentType: depr.AIRichResponseSubMessageType_AI_RICH_RESPONSE_CODE,
		codeBlock: &depr.AIRichResponseCodeMetadata{
			CodeLanguage: &lang,
			CodeBlocks:   blocks,
		},
	})
	return r
}

func (r *RichComposer) AppendTableSegment(title string, rows []*depr.AIRichResponseTableMetadata_AIRichResponseTableRow) *RichComposer {
	r.parts = append(r.parts, &msgSegment{
		segmentType: depr.AIRichResponseSubMessageType_AI_RICH_RESPONSE_TABLE,
		tableData: &depr.AIRichResponseTableMetadata{
			Title: &title,
			Rows:  rows,
		},
	})
	return r
}

type FlowActionComposer struct {
	sender               Messager
	topTitle             string
	subHeader            string
	middleBody           string
	bottomFooter         string
	attachments          map[string]any
	actions              []*waProto.InteractiveMessage_NativeFlowMessage_NativeFlowButton
	activeSelectionIndex int
	activeSectionIndex   int
	properties           map[string]any
}

func NewActionComposer(sender Messager) *FlowActionComposer {
	return &FlowActionComposer{
		sender:               sender,
		activeSelectionIndex: -1,
		activeSectionIndex:   -1,
		properties:           make(map[string]any),
		attachments:          make(map[string]any),
	}
}

func (f *FlowActionComposer) WithTitle(s string) *FlowActionComposer {
	f.topTitle = s
	return f
}

func (f *FlowActionComposer) WithSubtitle(s string) *FlowActionComposer {
	f.subHeader = s
	return f
}

func (f *FlowActionComposer) WithBody(s string) *FlowActionComposer {
	f.middleBody = s
	return f
}

func (f *FlowActionComposer) WithFooter(s string) *FlowActionComposer {
	f.bottomFooter = s
	return f
}

func (f *FlowActionComposer) WithImageMedia(path any) *FlowActionComposer {
	f.attachments["image"] = path
	return f
}

func (f *FlowActionComposer) WithVideoMedia(path any) *FlowActionComposer {
	f.attachments["video"] = path
	return f
}

func (f *FlowActionComposer) WithDocumentMedia(path any) *FlowActionComposer {
	f.attachments["document"] = path
	return f
}

func (f *FlowActionComposer) WithMediaData(obj map[string]any) *FlowActionComposer {
	f.attachments = obj
	return f
}

func (f *FlowActionComposer) ResetActions() *FlowActionComposer {
	f.actions = nil
	return f
}

func (f *FlowActionComposer) WithParams(obj map[string]any) *FlowActionComposer {
	f.properties = obj
	return f
}

func (f *FlowActionComposer) RegisterAction(name string, params any) *FlowActionComposer {
	var jsonStr string
	switch p := params.(type) {
	case string:
		jsonStr = p
	default:
		bs, err := json.Marshal(p)
		if err == nil {
			jsonStr = string(bs)
		}
	}
	f.actions = append(f.actions, &waProto.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
		Name:             proto.String(name),
		ButtonParamsJSON: proto.String(jsonStr),
	})
	return f
}

func (f *FlowActionComposer) AddReplyAction(displayText, id string, options ...map[string]any) *FlowActionComposer {
	payload := map[string]any{
		"display_text": displayText,
		"id":           id,
	}
	if len(options) > 0 && options[0] != nil {
		for k, v := range options[0] {
			payload[k] = v
		}
	}
	return f.RegisterAction("quick_reply", payload)
}

func (f *FlowActionComposer) AddURLAction(displayText, url string, webviewInteraction bool, options ...map[string]any) *FlowActionComposer {
	payload := map[string]any{
		"display_text":        displayText,
		"url":                 url,
		"webview_interaction": webviewInteraction,
	}
	if len(options) > 0 && options[0] != nil {
		for k, v := range options[0] {
			payload[k] = v
		}
	}
	return f.RegisterAction("cta_url", payload)
}

func (f *FlowActionComposer) AddCopyAction(displayText, copyCode string, options ...map[string]any) *FlowActionComposer {
	payload := map[string]any{
		"display_text": displayText,
		"copy_code":    copyCode,
	}
	if len(options) > 0 && options[0] != nil {
		for k, v := range options[0] {
			payload[k] = v
		}
	}
	return f.RegisterAction("cta_copy", payload)
}

func (f *FlowActionComposer) AddSingleSelection(title string, options ...map[string]any) *FlowActionComposer {
	payload := map[string]any{
		"title":    title,
		"sections": []any{},
	}
	f.RegisterAction("single_select", payload)
	f.activeSelectionIndex = len(f.actions) - 1
	f.activeSectionIndex = -1
	return f
}

func (f *FlowActionComposer) CreateSection(title string, highlightLabel ...string) *FlowActionComposer {
	if f.activeSelectionIndex == -1 {
		return f
	}
	btn := f.actions[f.activeSelectionIndex]
	var payload map[string]any
	_ = json.Unmarshal([]byte(btn.GetButtonParamsJSON()), &payload)

	sections, _ := payload["sections"].([]any)
	newSec := map[string]any{
		"title": title,
		"rows":  []any{},
	}
	if len(highlightLabel) > 0 && highlightLabel[0] != "" {
		newSec["highlight_label"] = highlightLabel[0]
	}
	sections = append(sections, newSec)
	payload["sections"] = sections

	bs, _ := json.Marshal(payload)
	btn.ButtonParamsJSON = proto.String(string(bs))

	f.activeSectionIndex = len(sections) - 1
	return f
}

func (f *FlowActionComposer) CreateRow(header, title, description, id string) *FlowActionComposer {
	if f.activeSelectionIndex == -1 || f.activeSectionIndex == -1 {
		return f
	}
	btn := f.actions[f.activeSelectionIndex]
	var payload map[string]any
	_ = json.Unmarshal([]byte(btn.GetButtonParamsJSON()), &payload)

	sections, _ := payload["sections"].([]any)
	sec, _ := sections[f.activeSectionIndex].(map[string]any)
	rows, _ := sec["rows"].([]any)
	rows = append(rows, map[string]any{
		"header":      header,
		"title":       title,
		"description": description,
		"id":          id,
	})
	sec["rows"] = rows
	sections[f.activeSectionIndex] = sec
	payload["sections"] = sections

	bs, _ := json.Marshal(payload)
	btn.ButtonParamsJSON = proto.String(string(bs))
	return f
}

func (f *FlowActionComposer) RenderInteractiveCard(ctx context.Context) (*waProto.InteractiveMessage, error) {
	header := &waProto.InteractiveMessage_Header{
		Title:              proto.String(f.topTitle),
		Subtitle:           proto.String(f.subHeader),
		HasMediaAttachment: proto.Bool(len(f.attachments) > 0),
	}

	if len(f.attachments) > 0 {
		m, err := uploadAndComposeInteractiveMedia(ctx, f.sender, f.attachments)
		if err != nil {
			return nil, err
		}
		if m != nil {
			switch v := m.(type) {
			case *waProto.InteractiveMessage_Header_ImageMessage:
				header.Media = v
			case *waProto.InteractiveMessage_Header_VideoMessage:
				header.Media = v
			case *waProto.InteractiveMessage_Header_DocumentMessage:
				header.Media = v
			}
		}
	}

	var messageParams string
	if len(f.properties) > 0 {
		bs, _ := json.Marshal(f.properties)
		messageParams = string(bs)
	}

	interactiveMsg := &waProto.InteractiveMessage{
		Header: header,
		Body: &waProto.InteractiveMessage_Body{
			Text: proto.String(f.middleBody),
		},
		Footer: &waProto.InteractiveMessage_Footer{
			Text: proto.String(f.bottomFooter),
		},
		InteractiveMessage: &waProto.InteractiveMessage_NativeFlowMessage_{
			NativeFlowMessage: &waProto.InteractiveMessage_NativeFlowMessage{
				Buttons:           f.actions,
				MessageParamsJSON: proto.String(messageParams),
			},
		},
	}

	return interactiveMsg, nil
}

func (f *FlowActionComposer) AssembleInteractive(ctx context.Context, evt *events.Message) (*waProto.Message, error) {
	card, err := f.RenderInteractiveCard(ctx)
	if err != nil {
		return nil, err
	}

	ci := &waProto.ContextInfo{
		IsForwarded:     proto.Bool(true),
		ForwardingScore: proto.Uint32(1),
		ForwardOrigin:   waProto.ContextInfo_META_AI.Enum(),
		ForwardedAiBotMessageInfo: &waAICommon.ForwardedAIBotMessageInfo{
			BotJID: proto.String("0@bot"),
		},
	}

	if evt != nil {
		ci.StanzaID = proto.String(evt.Info.ID)
		ci.Participant = proto.String(evt.Info.Sender.String())
		ci.QuotedMessage = evt.Message
	}

	card.ContextInfo = ci

	return &waProto.Message{
		InteractiveMessage: card,
	}, nil
}

func (f *FlowActionComposer) DispatchInteractive(ctx context.Context, to types.JID, evt *events.Message) error {
	msg, err := f.AssembleInteractive(ctx, evt)
	if err != nil {
		return err
	}

	additionalNodes := []waBinary.Node{
		{
			Tag:   "biz",
			Attrs: waBinary.Attrs{},
			Content: []waBinary.Node{
				{
					Tag:   "interactive",
					Attrs: waBinary.Attrs{"type": "native_flow", "v": "1"},
					Content: []waBinary.Node{
						{
							Tag:   "native_flow",
							Attrs: waBinary.Attrs{"v": "9", "name": "mixed"},
						},
					},
				},
			},
		},
	}

	_, err = f.sender.SendMessage(ctx, to, msg, whatsmeow.SendRequestExtra{
		AdditionalNodes: &additionalNodes,
	})
	return err
}

type SimpleFlowComposer struct {
	dispatcher    Messager
	cardTitle     string
	cardSubtitle  string
	cardBody      string
	cardFooter    string
	cardThumbnail any
	cardButtons   []*waProto.InteractiveMessage_NativeFlowMessage_NativeFlowButton
}

func NewSimpleFlow(dispatcher Messager) *SimpleFlowComposer {
	return &SimpleFlowComposer{dispatcher: dispatcher}
}

func (s *SimpleFlowComposer) WithTitle(str string) *SimpleFlowComposer {
	s.cardTitle = str
	return s
}

func (s *SimpleFlowComposer) WithSubtitle(str string) *SimpleFlowComposer {
	s.cardSubtitle = str
	return s
}

func (s *SimpleFlowComposer) WithBody(str string) *SimpleFlowComposer {
	s.cardBody = str
	return s
}

func (s *SimpleFlowComposer) WithFooter(str string) *SimpleFlowComposer {
	s.cardFooter = str
	return s
}

func (s *SimpleFlowComposer) WithThumbnail(path any) *SimpleFlowComposer {
	s.cardThumbnail = path
	return s
}

func (s *SimpleFlowComposer) AddQuickReply(displayText string, buttonId ...string) *SimpleFlowComposer {
	id := generateCryptoUUID()
	if len(buttonId) > 0 && buttonId[0] != "" {
		id = buttonId[0]
	}
	payload := map[string]any{
		"display_text": displayText,
		"id":           id,
	}
	bs, _ := json.Marshal(payload)
	s.cardButtons = append(s.cardButtons, &waProto.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
		Name:             proto.String("quick_reply"),
		ButtonParamsJSON: proto.String(string(bs)),
	})
	return s
}

func (s *SimpleFlowComposer) AssembleFlow(ctx context.Context, evt *events.Message) (*waProto.Message, error) {
	header := &waProto.InteractiveMessage_Header{
		Title:              proto.String(s.cardTitle),
		Subtitle:           proto.String(s.cardSubtitle),
		HasMediaAttachment: proto.Bool(s.cardThumbnail != nil),
	}

	if s.cardThumbnail != nil {
		mediaMap := map[string]any{"image": s.cardThumbnail}
		m, err := uploadAndComposeInteractiveMedia(ctx, s.dispatcher, mediaMap)
		if err != nil {
			return nil, err
		}
		if m != nil {
			switch v := m.(type) {
			case *waProto.InteractiveMessage_Header_ImageMessage:
				header.Media = v
			}
		}
	}

	ci := &waProto.ContextInfo{
		IsForwarded:     proto.Bool(true),
		ForwardingScore: proto.Uint32(1),
		ForwardOrigin:   waProto.ContextInfo_META_AI.Enum(),
		ForwardedAiBotMessageInfo: &waAICommon.ForwardedAIBotMessageInfo{
			BotJID: proto.String("0@bot"),
		},
	}

	if evt != nil {
		ci.StanzaID = proto.String(evt.Info.ID)
		ci.Participant = proto.String(evt.Info.Sender.String())
		ci.QuotedMessage = evt.Message
	}

	interactiveMsg := &waProto.InteractiveMessage{
		Header: header,
		Body: &waProto.InteractiveMessage_Body{
			Text: proto.String(s.cardBody),
		},
		Footer: &waProto.InteractiveMessage_Footer{
			Text: proto.String(s.cardFooter),
		},
		InteractiveMessage: &waProto.InteractiveMessage_NativeFlowMessage_{
			NativeFlowMessage: &waProto.InteractiveMessage_NativeFlowMessage{
				Buttons: s.cardButtons,
			},
		},
		ContextInfo: ci,
	}

	return &waProto.Message{
		InteractiveMessage: interactiveMsg,
	}, nil
}

func (s *SimpleFlowComposer) DispatchFlow(ctx context.Context, to types.JID, evt *events.Message) error {
	msg, err := s.AssembleFlow(ctx, evt)
	if err != nil {
		return err
	}

	additionalNodes := []waBinary.Node{
		{
			Tag:   "biz",
			Attrs: waBinary.Attrs{},
			Content: []waBinary.Node{
				{
					Tag:   "interactive",
					Attrs: waBinary.Attrs{"type": "native_flow", "v": "1"},
					Content: []waBinary.Node{
						{
							Tag:   "native_flow",
							Attrs: waBinary.Attrs{"v": "9", "name": "mixed"},
						},
					},
				},
			},
		},
	}

	_, err = s.dispatcher.SendMessage(ctx, to, msg, whatsmeow.SendRequestExtra{
		AdditionalNodes: &additionalNodes,
	})
	return err
}

type MessageCarouselComposer struct {
	messenger      Messager
	carouselBody   string
	carouselFooter string
	carouselCards  []*waProto.InteractiveMessage
}

func NewCarouselComposer(messenger Messager) *MessageCarouselComposer {
	return &MessageCarouselComposer{messenger: messenger}
}

func (m *MessageCarouselComposer) WithBody(s string) *MessageCarouselComposer {
	m.carouselBody = s
	return m
}

func (m *MessageCarouselComposer) WithFooter(s string) *MessageCarouselComposer {
	m.carouselFooter = s
	return m
}

func (m *MessageCarouselComposer) AppendCard(card *waProto.InteractiveMessage) *MessageCarouselComposer {
	if card != nil {
		m.carouselCards = append(m.carouselCards, card)
	}
	return m
}

func (m *MessageCarouselComposer) AssembleCarousel(evt *events.Message) *waProto.Message {
	ci := &waProto.ContextInfo{
		IsForwarded:     proto.Bool(true),
		ForwardingScore: proto.Uint32(1),
		ForwardOrigin:   waProto.ContextInfo_META_AI.Enum(),
		ForwardedAiBotMessageInfo: &waAICommon.ForwardedAIBotMessageInfo{
			BotJID: proto.String("0@bot"),
		},
	}

	if evt != nil {
		ci.StanzaID = proto.String(evt.Info.ID)
		ci.Participant = proto.String(evt.Info.Sender.String())
		ci.QuotedMessage = evt.Message
	}

	interactiveMsg := &waProto.InteractiveMessage{
		Header: &waProto.InteractiveMessage_Header{
			HasMediaAttachment: proto.Bool(false),
		},
		Body: &waProto.InteractiveMessage_Body{
			Text: proto.String(m.carouselBody),
		},
		Footer: &waProto.InteractiveMessage_Footer{
			Text: proto.String(m.carouselFooter),
		},
		ContextInfo: ci,
		InteractiveMessage: &waProto.InteractiveMessage_CarouselMessage_{
			CarouselMessage: &waProto.InteractiveMessage_CarouselMessage{
				Cards: m.carouselCards,
			},
		},
	}

	return &waProto.Message{
		InteractiveMessage: interactiveMsg,
	}
}

func (m *MessageCarouselComposer) DispatchCarousel(ctx context.Context, to types.JID, evt *events.Message) error {
	msg := m.AssembleCarousel(evt)

	additionalNodes := []waBinary.Node{
		{
			Tag:   "biz",
			Attrs: waBinary.Attrs{},
			Content: []waBinary.Node{
				{
					Tag:   "interactive",
					Attrs: waBinary.Attrs{"type": "native_flow", "v": "1"},
					Content: []waBinary.Node{
						{
							Tag:   "native_flow",
							Attrs: waBinary.Attrs{"v": "9", "name": "mixed"},
						},
					},
				},
			},
		},
	}

	_, err := m.messenger.SendMessage(ctx, to, msg, whatsmeow.SendRequestExtra{
		AdditionalNodes: &additionalNodes,
	})
	return err
}

func transmitMediaAndCache(ctx context.Context, cli *whatsmeow.Client, source string, mediaType whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
	if source == "" {
		return whatsmeow.UploadResponse{}, fmt.Errorf("empty media source")
	}

	if cached, ok := mediaStoreCache.Load("enc_" + source); ok {
		if res, ok := cached.(whatsmeow.UploadResponse); ok {
			return res, nil
		}
	}

	data := downloadSourceData(source)
	if len(data) == 0 {
		return whatsmeow.UploadResponse{}, fmt.Errorf("unable to retrieve bytes from source: %s", source)
	}

	uploaded, err := cli.Upload(ctx, data, mediaType)
	if err != nil {
		return whatsmeow.UploadResponse{}, err
	}

	mediaStoreCache.Store("enc_"+source, uploaded)
	return uploaded, nil
}

func transmitNewsletterMediaAndCache(ctx context.Context, cli *whatsmeow.Client, source string, mediaType whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
	if source == "" {
		return whatsmeow.UploadResponse{}, fmt.Errorf("empty media source")
	}

	if cached, ok := mediaStoreCache.Load("nl_" + source); ok {
		if res, ok := cached.(whatsmeow.UploadResponse); ok {
			return res, nil
		}
	}

	data := downloadSourceData(source)
	if len(data) == 0 {
		return whatsmeow.UploadResponse{}, fmt.Errorf("unable to retrieve bytes from source: %s", source)
	}

	uploaded, err := cli.UploadNewsletter(ctx, data, mediaType)
	if err != nil {
		return whatsmeow.UploadResponse{}, err
	}

	mediaStoreCache.Store("nl_"+source, uploaded)
	return uploaded, nil
}

func downloadSourceData(val any) []byte {
	switch v := val.(type) {
	case []byte:
		return v
	case string:
		if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
			client := &http.Client{
				Timeout: 15 * time.Second,
				Transport: &http.Transport{
					TLSNextProto: make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
				},
			}
			resp, err := client.Get(v)
			if err != nil {
				return nil
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				data, _ := io.ReadAll(resp.Body)
				return data
			}
		} else {
			data, err := os.ReadFile(v)
			if err == nil {
				return data
			}
		}
	case map[string]any:
		if urlVal, ok := v["url"].(string); ok {
			return downloadSourceData(urlVal)
		}
	}
	return nil
}

func uploadAndComposeInteractiveMedia(ctx context.Context, client Messager, media map[string]any) (any, error) {
	if media == nil {
		return nil, nil
	}

	cli, ok := client.(*whatsmeow.Client)
	if !ok {
		return nil, fmt.Errorf("client is not whatsmeow.Client")
	}

	var sourceStr string
	var mediaType whatsmeow.MediaType
	var key string
	var rawData []byte

	if img, ok := media["image"]; ok {
		key = "image"
		mediaType = whatsmeow.MediaImage
		if s, ok := img.(string); ok {
			sourceStr = s
		} else if b, ok := img.([]byte); ok {
			rawData = b
		}
	} else if vid, ok := media["video"]; ok {
		key = "video"
		mediaType = whatsmeow.MediaVideo
		if s, ok := vid.(string); ok {
			sourceStr = s
		} else if b, ok := vid.([]byte); ok {
			rawData = b
		}
	} else if doc, ok := media["document"]; ok {
		key = "document"
		mediaType = whatsmeow.MediaDocument
		if s, ok := doc.(string); ok {
			sourceStr = s
		} else if b, ok := doc.([]byte); ok {
			rawData = b
		}
	}

	var uploaded whatsmeow.UploadResponse
	var err error

	if sourceStr != "" {
		uploaded, err = transmitMediaAndCache(ctx, cli, sourceStr, mediaType)
	} else if len(rawData) > 0 {
		uploaded, err = cli.Upload(ctx, rawData, mediaType)
	} else {
		return nil, fmt.Errorf("no media data found or invalid format")
	}

	if err != nil {
		return nil, err
	}

	switch key {
	case "image":
		return &waProto.InteractiveMessage_Header_ImageMessage{
			ImageMessage: &waProto.ImageMessage{
				URL:           proto.String(uploaded.URL),
				DirectPath:    proto.String(uploaded.DirectPath),
				MediaKey:      uploaded.MediaKey,
				FileEncSHA256: uploaded.FileEncSHA256,
				FileSHA256:    uploaded.FileSHA256,
				FileLength:    proto.Uint64(uploaded.FileLength),
				Mimetype:      proto.String("image/png"),
			},
		}, nil
	case "video":
		return &waProto.InteractiveMessage_Header_VideoMessage{
			VideoMessage: &waProto.VideoMessage{
				URL:           proto.String(uploaded.URL),
				DirectPath:    proto.String(uploaded.DirectPath),
				MediaKey:      uploaded.MediaKey,
				FileEncSHA256: uploaded.FileEncSHA256,
				FileSHA256:    uploaded.FileSHA256,
				FileLength:    proto.Uint64(uploaded.FileLength),
				Mimetype:      proto.String("video/mp4"),
			},
		}, nil
	case "document":
		return &waProto.InteractiveMessage_Header_DocumentMessage{
			DocumentMessage: &waProto.DocumentMessage{
				URL:           proto.String(uploaded.URL),
				DirectPath:    proto.String(uploaded.DirectPath),
				MediaKey:      uploaded.MediaKey,
				FileEncSHA256: uploaded.FileEncSHA256,
				FileSHA256:    uploaded.FileSHA256,
				FileLength:    proto.Uint64(uploaded.FileLength),
				Mimetype:      proto.String("application/pdf"),
			},
		}, nil
	}

	return nil, fmt.Errorf("unsupported media type")
}

func traverseAndProcessLayoutMedia(ctx context.Context, client Messager, val any) any {
	if ctx == nil || client == nil {
		return val
	}
	cli, ok := client.(*whatsmeow.Client)
	if !ok {
		return val
	}

	switch v := val.(type) {
	case map[string]any:
		if urlStr, ok := v["url"].(string); ok && (strings.HasPrefix(urlStr, "http://") || strings.HasPrefix(urlStr, "https://")) && !strings.Contains(urlStr, ".whatsapp.net") {
			mime, _ := v["mime_type"].(string)
			mediaType := whatsmeow.MediaImage
			if strings.HasPrefix(mime, "video/") {
				mediaType = whatsmeow.MediaVideo
			} else if strings.HasPrefix(mime, "audio/") {
				mediaType = whatsmeow.MediaAudio
			}

			if uploaded, err := transmitNewsletterMediaAndCache(ctx, cli, urlStr, mediaType); err == nil && uploaded.URL != "" {
				v["url"] = uploaded.URL
			}
		}

		if rawMedia, ok := v["raw_media"].(string); ok && (strings.HasPrefix(rawMedia, "http://") || strings.HasPrefix(rawMedia, "https://")) && !strings.Contains(rawMedia, ".whatsapp.net") {
			if uploaded, err := transmitNewsletterMediaAndCache(ctx, cli, rawMedia, whatsmeow.MediaImage); err == nil && uploaded.URL != "" {
				v["raw_media"] = uploaded.URL
			}
		}

		for k, child := range v {
			v[k] = traverseAndProcessLayoutMedia(ctx, client, child)
		}
		return v

	case []any:
		for i, child := range v {
			v[i] = traverseAndProcessLayoutMedia(ctx, client, child)
		}
		return v

	case []map[string]any:
		for i, child := range v {
			res := traverseAndProcessLayoutMedia(ctx, client, child)
			if mapped, ok := res.(map[string]any); ok {
				v[i] = mapped
			}
		}
		return v
	}

	return val
}

func ComposeLayout(name string, data any, extra map[string]any) map[string]any {
	return buildLayoutData(name, data, extra)
}

func DecomposeCode(code, lang string) ([]*depr.AIRichResponseCodeMetadata_AIRichResponseCodeBlock, []map[string]any) {
	return tokenizeCodeString(code, lang)
}

func ConvertTableToMetadata(arr [][]string) (string, []*depr.AIRichResponseTableMetadata_AIRichResponseTableRow, []map[string]any) {
	return formatTableMetadata(arr)
}

func parseInlineEntities(text string) (string, []embeddedEntity) {
	var inlineEntities []embeddedEntity
	var result strings.Builder
	runes := []rune(text)
	n := len(runes)
	var stack []int
	last := 0

	citationIndex := 1
	hyperlinkIndex := 0
	latexIndex := 0

	for i := 0; i < n; i++ {
		if runes[i] == '[' && (i == 0 || runes[i-1] != '\\') {
			stack = append(stack, i)
		} else if runes[i] == ']' && (i == 0 || runes[i-1] != '\\') {
			if len(stack) == 0 {
				continue
			}
			start := stack[len(stack)-1]
			stack = stack[:len(stack)-1]

			if i+1 < n && (runes[i+1] == '(' || runes[i+1] == '<') {
				open := runes[i+1]
				var close rune
				if open == '(' {
					close = ')'
				} else {
					close = '>'
				}

				end := i + 2
				depth := 1
				for end < n && depth > 0 {
					if runes[end] == open && runes[end-1] != '\\' {
						depth++
					} else if runes[end] == close && runes[end-1] != '\\' {
						depth--
					}
					end++
				}

				if depth > 0 {
					continue
				}

				raw := strings.TrimSpace(string(runes[start+1 : i]))
				val := strings.TrimSpace(string(runes[i+2 : end-1]))

				var key string
				var tag string
				var entity *embeddedEntity

				if open == '<' {
					parts := strings.Split(raw, "|")
					txt := ""
					widthStr := ""
					heightStr := ""
					fontHeightStr := ""
					paddingStr := ""

					if len(parts) > 0 {
						txt = parts[0]
					}
					if len(parts) > 1 {
						widthStr = parts[1]
					}
					if len(parts) > 2 {
						heightStr = parts[2]
					}
					if len(parts) > 3 {
						fontHeightStr = parts[3]
					}
					if len(parts) > 4 {
						paddingStr = parts[4]
					}

					key = fmt.Sprintf("WA_LATEX_%d", latexIndex)
					latexIndex++

					displayTxt := txt
					if displayTxt == "" {
						displayTxt = "image"
					}
					tag = fmt.Sprintf("{{%s}}%s{{/%s}}", key, displayTxt, key)

					w := 0
					h := 0
					if widthStr != "" {
						fmt.Sscan(widthStr, &w)
					}
					if heightStr != "" {
						fmt.Sscan(heightStr, &h)
					}

					if w == 0 || h == 0 {
						w, h = getRemoteImageDimensions(val)
					}
					if w == 0 {
						w = 400
					}
					if h == 0 {
						h = 120
					}

					fh := 83.333333333333
					if fontHeightStr != "" {
						fmt.Sscan(fontHeightStr, &fh)
					}

					pad := 15.0
					if paddingStr != "" {
						fmt.Sscan(paddingStr, &pad)
					}

					entity = &embeddedEntity{
						Token: key,
						Info: map[string]any{
							"latex_expression": txt,
							"latex_image": map[string]any{
								"url":    val,
								"width":  w,
								"height": h,
							},
							"font_height": fh,
							"padding":     pad,
							"__typename":  "GenAILatexItem",
						},
					}
				} else if raw != "" {
					trusted := !strings.HasPrefix(val, "!")
					if !trusted {
						val = val[1:]
					}

					key = fmt.Sprintf("WA_HYPERLINK_%d", hyperlinkIndex)
					hyperlinkIndex++
					tag = fmt.Sprintf("{{%s}}%s{{/%s}}", key, val, key)

					entity = &embeddedEntity{
						Token: key,
						Info: map[string]any{
							"display_name": raw,
							"is_trusted":   trusted,
							"url":          val,
							"__typename":   "GenAIInlineLinkItem",
						},
					}
				} else {
					key = fmt.Sprintf("WA_CITATION_%d", citationIndex-1)
					tag = fmt.Sprintf("{{%s}}%s{{/%s}}", key, val, key)

					entity = &embeddedEntity{
						Token: key,
						Info: map[string]any{
							"reference_id":           citationIndex,
							"reference_url":          val,
							"reference_title":        val,
							"reference_display_name": val,
							"sources":                []any{},
							"__typename":             "GenAISearchCitationItem",
						},
					}
					citationIndex++
				}

				result.WriteString(string(runes[last:start]))
				result.WriteString(tag)
				last = end
				if entity != nil {
					inlineEntities = append(inlineEntities, *entity)
				}
				i = end - 1
			}
		}
	}
	result.WriteString(string(runes[last:]))
	return result.String(), inlineEntities
}

func getRemoteImageDimensions(urlStr string) (int, int) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(urlStr)
	if err != nil {
		return 0, 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, 0
	}
	cfg, _, err := image.DecodeConfig(resp.Body)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

func buildLayoutData(name string, data any, extra map[string]any) map[string]any {
	res := make(map[string]any)
	for k, v := range extra {
		res[k] = v
	}

	viewModel := make(map[string]any)
	viewModel["__typename"] = fmt.Sprintf("GenAI%sLayoutViewModel", name)

	isSlice := false
	if data != nil {
		switch data.(type) {
		case []any:
			isSlice = true
		case []map[string]any:
			isSlice = true
		case []string:
			isSlice = true
		default:
			val := reflect.ValueOf(data)
			if val.Kind() == reflect.Slice || val.Kind() == reflect.Array {
				isSlice = true
			}
		}
	}

	if isSlice {
		viewModel["primitives"] = data
	} else {
		viewModel["primitive"] = data
	}

	res["view_model"] = viewModel
	return res
}

func generateCryptoUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[1:3], b[2:4], b[3:5], b[4:6])
}

type codeToken struct {
	content string
	typ     int
}

func tokenizeCodeString(code, lang string) ([]*depr.AIRichResponseCodeMetadata_AIRichResponseCodeBlock, []map[string]any) {
	lang = strings.ToLower(lang)
	if lang == "" || lang == "txt" || lang == "text" || lang == "plaintext" {
		return []*depr.AIRichResponseCodeMetadata_AIRichResponseCodeBlock{{
				CodeContent:   proto.String(code),
				HighlightType: depr.AIRichResponseCodeMetadata_AI_RICH_RESPONSE_CODE_HIGHLIGHT_DEFAULT.Enum(),
			}}, []map[string]any{{
				"content": code,
				"type":    "DEFAULT",
			}}
	}

	keywords := keywordsMap[lang]
	var tokens []codeToken
	n := len(code)
	i := 0

	isSpace := func(c byte) bool {
		return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
	}
	isDigit := func(c byte) bool {
		return c >= '0' && c <= '9'
	}
	isLetter := func(c byte) bool {
		return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || c == '$'
	}
	isIdentifierPart := func(c byte) bool {
		if isLetter(c) || isDigit(c) {
			return true
		}
		if lang == "css" || lang == "html" {
			return c == '-' || c == ':'
		}
		return false
	}

	addToken := func(content string, typ int) {
		if content == "" {
			return
		}
		if len(tokens) > 0 && tokens[len(tokens)-1].typ == typ {
			tokens[len(tokens)-1].content += content
		} else {
			tokens = append(tokens, codeToken{content: content, typ: typ})
		}
	}

	for i < n {
		c := code[i]

		if isSpace(c) {
			start := i
			for i < n && isSpace(code[i]) {
				i++
			}
			addToken(code[start:i], 0)
			continue
		}

		if (c == '/' && i+1 < n && code[i+1] == '/') || (c == '#' && (lang == "python" || lang == "bash")) {
			start := i
			for i < n && code[i] != '\n' {
				i++
			}
			addToken(code[start:i], 5)
			continue
		}

		if c == '"' || c == '\'' || c == '`' {
			start := i
			quote := c
			i++
			for i < n {
				if code[i] == '\\' && i+1 < n {
					i += 2
				} else if code[i] == quote {
					i++
					break
				} else {
					i++
				}
			}
			addToken(code[start:i], 3)
			continue
		}

		if isDigit(c) {
			start := i
			for i < n && (isDigit(code[i]) || code[i] == '.' || code[i] == '_') {
				i++
			}
			addToken(code[start:i], 4)
			continue
		}

		if isLetter(c) {
			start := i
			for i < n && isIdentifierPart(code[i]) {
				i++
			}
			word := code[start:i]
			typ := 0

			if keywords != nil && keywords[word] {
				typ = 1
			} else if lang == "css" {
				j := i
				for j < n && isSpace(code[j]) {
					j++
				}
				if j < n && code[j] == ':' {
					typ = 1
				}
			} else if lang == "html" {
				p := start - 1
				for p >= 0 && isSpace(code[p]) {
					p--
				}
				if p >= 0 && (code[p] == '<' || (code[p] == '/' && p-1 >= 0 && code[p-1] == '<')) {
					typ = 1
				}
			}

			if typ == 0 {
				j := i
				for j < n && isSpace(code[j]) {
					j++
				}
				if j < n && code[j] == '(' {
					typ = 2
				}
			}

			addToken(word, typ)
			continue
		}

		addToken(string(c), 0)
		i++
	}

	typeMap := map[int]string{
		0: "DEFAULT",
		1: "KEYWORD",
		2: "METHOD",
		3: "STR",
		4: "NUMBER",
		5: "COMMENT",
	}

	var protoBlocks []*depr.AIRichResponseCodeMetadata_AIRichResponseCodeBlock
	var unifiedBlocks []map[string]any

	for _, tok := range tokens {
		protoBlocks = append(protoBlocks, &depr.AIRichResponseCodeMetadata_AIRichResponseCodeBlock{
			CodeContent:   proto.String(tok.content),
			HighlightType: depr.AIRichResponseCodeMetadata_AIRichResponseCodeHighlightType(tok.typ).Enum(),
		})
		unifiedBlocks = append(unifiedBlocks, map[string]any{
			"content": tok.content,
			"type":    typeMap[tok.typ],
		})
	}

	return protoBlocks, unifiedBlocks
}

func formatTableMetadata(arr [][]string) (string, []*depr.AIRichResponseTableMetadata_AIRichResponseTableRow, []map[string]any) {
	if len(arr) == 0 {
		return "", nil, nil
	}
	header := arr[0]
	rows := arr[1:]

	maxCols := len(header)
	for _, r := range rows {
		if len(r) > maxCols {
			maxCols = len(r)
		}
	}

	normalizeRow := func(r []string) []string {
		res := make([]string, maxCols)
		copy(res, r)
		return res
	}

	var unifiedRows []map[string]any

	buildRowMap := func(cells []string, isHeader bool) map[string]any {
		var markdownCells []map[string]any
		hasInlineEntities := false

		for _, cell := range cells {
			txt, entities := parseInlineEntities(cell)
			cellMap := map[string]any{"text": txt}
			if len(entities) > 0 {
				cellMap["inline_entities"] = entities
				hasInlineEntities = true
			}
			markdownCells = append(markdownCells, cellMap)
		}

		rowMap := map[string]any{
			"is_header": isHeader,
			"cells":     cells,
		}
		if hasInlineEntities {
			rowMap["markdown_cells"] = markdownCells
		}
		return rowMap
	}

	headerNorm := normalizeRow(header)
	unifiedRows = append(unifiedRows, buildRowMap(headerNorm, true))

	for _, r := range rows {
		norm := normalizeRow(r)
		unifiedRows = append(unifiedRows, buildRowMap(norm, false))
	}

	var protoRows []*depr.AIRichResponseTableMetadata_AIRichResponseTableRow
	for i, r := range arr {
		protoRows = append(protoRows, &depr.AIRichResponseTableMetadata_AIRichResponseTableRow{
			Items:     normalizeRow(r),
			IsHeading: proto.Bool(i == 0),
		})
	}

	return "", protoRows, unifiedRows
}

var keywordsMap = map[string]map[string]bool{
	"javascript": {
		"break": true, "case": true, "catch": true, "continue": true, "debugger": true, "delete": true,
		"do": true, "else": true, "finally": true, "for": true, "function": true, "if": true,
		"in": true, "instanceof": true, "new": true, "return": true, "switch": true, "this": true,
		"throw": true, "try": true, "typeof": true, "var": true, "void": true, "while": true,
		"with": true, "true": true, "false": true, "null": true, "undefined": true, "class": true,
		"const": true, "let": true, "super": true, "extends": true, "export": true, "import": true,
		"yield": true, "static": true, "constructor": true, "async": true, "await": true, "get": true, "set": true,
	},
	"typescript": {
		"abstract": true, "any": true, "as": true, "asserts": true, "bigint": true, "boolean": true,
		"declare": true, "enum": true, "implements": true, "infer": true, "interface": true, "is": true,
		"keyof": true, "module": true, "namespace": true, "never": true, "readonly": true, "require": true,
		"number": true, "object": true, "override": true, "private": true, "protected": true, "public": true,
		"satisfies": true, "string": true, "symbol": true, "type": true, "unknown": true, "using": true,
		"from": true, "break": true, "case": true, "catch": true, "continue": true, "do": true,
		"else": true, "finally": true, "for": true, "function": true, "if": true, "new": true,
		"return": true, "switch": true, "this": true, "throw": true, "try": true, "var": true,
		"void": true, "while": true, "class": true, "const": true, "let": true, "extends": true,
		"import": true, "export": true, "async": true, "await": true,
	},
	"python": {
		"False": true, "None": true, "True": true, "and": true, "as": true, "assert": true,
		"async": true, "await": true, "break": true, "class": true, "continue": true, "def": true,
		"del": true, "elif": true, "else": true, "except": true, "finally": true, "for": true,
		"from": true, "global": true, "if": true, "import": true, "in": true, "is": true,
		"lambda": true, "nonlocal": true, "not": true, "or": true, "pass": true, "raise": true,
		"return": true, "try": true, "while": true, "with": true, "yield": true,
	},
	"java": {
		"abstract": true, "assert": true, "boolean": true, "break": true, "byte": true, "case": true,
		"catch": true, "char": true, "class": true, "const": true, "continue": true, "default": true,
		"do": true, "double": true, "else": true, "enum": true, "extends": true, "final": true,
		"finally": true, "float": true, "for": true, "goto": true, "if": true, "implements": true,
		"import": true, "instanceof": true, "int": true, "interface": true, "long": true, "native": true,
		"new": true, "package": true, "private": true, "protected": true, "public": true, "return": true,
		"short": true, "static": true, "strictfp": true, "super": true, "switch": true, "synchronized": true,
		"this": true, "throw": true, "throws": true, "transient": true, "try": true, "void": true,
		"volatile": true, "while": true,
	},
	"golang": {
		"break": true, "case": true, "chan": true, "const": true, "continue": true, "default": true,
		"defer": true, "else": true, "fallthrough": true, "for": true, "func": true, "go": true,
		"goto": true, "if": true, "import": true, "interface": true, "map": true, "package": true,
		"range": true, "return": true, "select": true, "struct": true, "switch": true, "type": true,
		"var": true,
	},
	"go": {
		"break": true, "case": true, "chan": true, "const": true, "continue": true, "default": true,
		"defer": true, "else": true, "fallthrough": true, "for": true, "func": true, "go": true,
		"goto": true, "if": true, "import": true, "interface": true, "map": true, "package": true,
		"range": true, "return": true, "select": true, "struct": true, "switch": true, "type": true,
		"var": true,
	},
	"c": {
		"auto": true, "break": true, "case": true, "char": true, "const": true, "continue": true,
		"default": true, "do": true, "double": true, "else": true, "enum": true, "extern": true,
		"float": true, "for": true, "goto": true, "if": true, "int": true, "long": true,
		"register": true, "return": true, "short": true, "signed": true, "sizeof": true, "static": true,
		"struct": true, "switch": true, "typedef": true, "union": true, "unsigned": true, "void": true,
		"volatile": true, "while": true,
	},
	"cpp": {
		"alignas": true, "alignof": true, "and": true, "auto": true, "bool": true, "break": true,
		"case": true, "catch": true, "class": true, "const": true, "constexpr": true, "continue": true,
		"delete": true, "do": true, "double": true, "else": true, "enum": true, "explicit": true,
		"export": true, "extern": true, "false": true, "float": true, "for": true, "friend": true,
		"if": true, "inline": true, "int": true, "long": true, "mutable": true, "namespace": true,
		"new": true, "noexcept": true, "nullptr": true, "operator": true, "private": true, "protected": true,
		"public": true, "return": true, "short": true, "signed": true, "sizeof": true, "static": true,
		"struct": true, "switch": true, "template": true, "this": true, "throw": true, "true": true,
		"try": true, "typedef": true, "typename": true, "union": true, "unsigned": true, "using": true,
		"virtual": true, "void": true, "while": true,
	},
	"php": {
		"abstract": true, "and": true, "array": true, "as": true, "break": true, "callable": true,
		"case": true, "catch": true, "class": true, "clone": true, "const": true, "continue": true,
		"declare": true, "default": true, "do": true, "echo": true, "else": true, "elseif": true,
		"empty": true, "enddeclare": true, "endfor": true, "endforeach": true, "endif": true, "endswitch": true,
		"endwhile": true, "extends": true, "final": true, "finally": true, "fn": true, "for": true,
		"foreach": true, "function": true, "global": true, "goto": true, "if": true, "implements": true,
		"include": true, "include_once": true, "instanceof": true, "interface": true, "match": true, "namespace": true,
		"new": true, "null": true, "or": true, "private": true, "protected": true, "public": true,
		"require": true, "require_once": true, "return": true, "static": true, "switch": true, "throw": true,
		"trait": true, "try": true, "use": true, "var": true, "while": true, "yield": true,
	},
	"rust": {
		"as": true, "break": true, "const": true, "continue": true, "crate": true, "else": true,
		"enum": true, "extern": true, "false": true, "fn": true, "for": true, "if": true,
		"impl": true, "in": true, "let": true, "loop": true, "match": true, "mod": true,
		"move": true, "mut": true, "pub": true, "ref": true, "return": true, "self": true,
		"Self": true, "static": true, "struct": true, "super": true, "trait": true, "true": true,
		"type": true, "unsafe": true, "use": true, "where": true, "while": true,
	},
	"html": {
		"html": true, "head": true, "body": true, "div": true, "span": true, "p": true,
		"a": true, "img": true, "video": true, "audio": true, "script": true, "style": true,
		"link": true, "meta": true, "form": true, "input": true, "button": true, "table": true,
		"tr": true, "td": true, "th": true, "ul": true, "ol": true, "li": true,
		"section": true, "article": true, "header": true, "footer": true, "nav": true, "main": true,
	},
	"bash": {
		"if": true, "then": true, "else": true, "elif": true, "fi": true, "for": true,
		"while": true, "do": true, "done": true, "case": true, "esac": true, "function": true,
		"in": true, "select": true, "until": true, "break": true, "continue": true, "return": true,
		"export": true, "readonly": true, "local": true, "declare": true,
	},
	"markdown": {
		"#": true, "##": true, "###": true, "####": true, "#####": true, "######": true,
	},
}
