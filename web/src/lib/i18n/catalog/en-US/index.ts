/**
 * The en-US catalog is the source of truth for message keys. To add a
 * namespace: create `<namespace>.ts` here, then add one line to this index.
 */
import { auth } from "./auth";
import { adminCommon } from "./adminCommon";
import { adminStats } from "./adminStats";
import { adminUsers } from "./adminUsers";
import { adminGroups } from "./adminGroups";
import { adminUpstreams } from "./adminUpstreams";
import { adminModels } from "./adminModels";
import { adminTools } from "./adminTools";
import { adminBilling } from "./adminBilling";
import { adminAnnounce } from "./adminAnnounce";
import { adminLogs } from "./adminLogs";
import { adminModeration } from "./adminModeration";
import { adminLogin } from "./adminLogin";
import { adminConversation } from "./adminConversation";
import { adminFiles } from "./adminFiles";
import { adminKb } from "./adminKb";
import { adminUserKeys } from "./adminUserKeys";
import { adminAbout } from "./adminAbout";
import { common } from "./common";
import { errors } from "./errors";
import { shell } from "./shell";
import { chatNav } from "./chatNav";
import { chatRich } from "./chatRich";
import { chatBlocks } from "./chatBlocks";
import { chatMsg } from "./chatMsg";
import { chatComposer } from "./chatComposer";
import { chatMisc } from "./chatMisc";
import { chatModel } from "./chatModel";
import { settings } from "./settings";
import { filesWs } from "./filesWs";
import { kbWs } from "./kbWs";
import { libraryWs } from "./libraryWs";
import { authFlow } from "./authFlow";
import { onboarding } from "./onboarding";
import { sharePage } from "./sharePage";
import { shellX } from "./shellX";
import { announcements } from "./announcements";
import { desktopSetup } from "./desktopSetup";
import { recentAll } from "./recentAll";

export const enUS = { chatNav, chatBlocks, chatRich, chatMsg, chatComposer, chatMisc, chatModel, settings, recentAll, desktopSetup, announcements, shellX, sharePage, onboarding, authFlow, common, errors, shell, auth, adminCommon, adminStats, adminUsers, adminGroups, adminUpstreams, adminModels, adminTools, adminBilling, adminAnnounce, adminLogs, adminModeration, adminLogin, adminConversation, adminFiles, adminKb, adminUserKeys, adminAbout, filesWs, kbWs, libraryWs };
export type Catalog = typeof enUS;
