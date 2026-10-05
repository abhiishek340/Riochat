import unittest

from pages import is_image, parse_page_ranges, suffix_of


class ParsePageRangesTest(unittest.TestCase):
    def test_range_and_single(self):
        self.assertEqual(parse_page_ranges("2-3", 3), [2, 3])
        self.assertEqual(parse_page_ranges("1, 3", 5), [1, 3])

    def test_nothing_valid_means_all_pages(self):
        self.assertEqual(parse_page_ranges("0,abc,9-2", 5), [1, 2, 3, 4, 5])
        self.assertEqual(parse_page_ranges("", 2), [1, 2])
        self.assertEqual(parse_page_ranges(None, 2), [1, 2])

    def test_clipping_and_collapse(self):
        self.assertEqual(parse_page_ranges("4-99", 5), [4, 5])
        self.assertEqual(parse_page_ranges("3-1", 5), [3])
        self.assertEqual(parse_page_ranges("2,2,1-2", 5), [1, 2])

    def test_empty_items_are_skipped(self):
        self.assertEqual(parse_page_ranges(",,2,", 3), [2])

    def test_zero_pages(self):
        self.assertEqual(parse_page_ranges("1-3", 0), [])


class SuffixTest(unittest.TestCase):
    def test_defaults_to_pdf(self):
        self.assertEqual(suffix_of(None), ".pdf")
        self.assertEqual(suffix_of("scan"), ".pdf")
        self.assertEqual(suffix_of("Photo.PNG"), ".png")

    def test_images(self):
        self.assertTrue(is_image(".TIFF"))
        self.assertFalse(is_image(".pdf"))
        self.assertFalse(is_image(".docx"))


if __name__ == "__main__":
    unittest.main()
